package service

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	pluginv1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
	pluginv2 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2"
	"github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2/wire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func hostCapability() PluginCapability {
	cap := testPreprocessCapability()
	cap.Permissions = []pluginv2.Permission{pluginv2.PermissionRequestMetadata, pluginv2.PermissionHostLog, pluginv2.PermissionHostMetric,
		pluginv2.PermissionHostConfig, pluginv2.PermissionSecretBroker, pluginv2.PermissionEventPublish}
	cap.TimeoutMS = 1000
	return cap
}
func TestPluginHostServicesEnforcePermissionsAndBoundedFields(t *testing.T) {
	cap := hostCapability()
	host := newPluginHostServices(&PluginInstallation{ID: 8, Manifest: PluginManifest{Capabilities: []PluginCapability{cap}}}, nil)
	defer host.Close()
	ctx := context.Background()
	_, err := host.Log(ctx, &wire.HostLogRequest{Capability: "other.v1", Level: "info", Code: "plugin.ready"})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	_, err = host.Log(ctx, &wire.HostLogRequest{Capability: cap.ID, Level: "info", Code: "Bearer private-secret\nforged"})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = host.Log(ctx, &wire.HostLogRequest{Capability: cap.ID, Level: "info", Code: "plugin.ready"})
	require.NoError(t, err)
	for _, value := range []float64{math.NaN(), math.Inf(1), -1, 1e7} {
		_, err = host.Metric(ctx, &wire.HostMetricRequest{Capability: cap.ID, Name: "requests", Value: value})
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	}
	_, err = host.Metric(ctx, &wire.HostMetricRequest{Capability: cap.ID, Name: "arbitrary-label", Value: 1})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = host.Metric(ctx, &wire.HostMetricRequest{Capability: cap.ID, Name: "requests", Value: 1})
	require.NoError(t, err)
	_, err = host.ReadConfig(ctx, &wire.HostCapabilityRequest{Capability: cap.ID})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	host.setConfig([]byte(`{"configured":true}`))
	cfg, err := host.ReadConfig(ctx, &wire.HostCapabilityRequest{Capability: cap.ID})
	require.NoError(t, err)
	cfg.ConfigJson[0] = '!'
	again, err := host.ReadConfig(ctx, &wire.HostCapabilityRequest{Capability: cap.ID})
	require.NoError(t, err)
	require.JSONEq(t, `{"configured":true}`, string(again.ConfigJson))
	_, err = host.ReadSecret(ctx, &wire.HostSecretRequest{Capability: cap.ID, Alias: "DATABASE_PASSWORD"})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	_, err = host.PublishEvent(ctx, &wire.HostEventRequest{Capability: cap.ID, Name: "policy.pass", Value: 1})
	require.NoError(t, err)
	require.Eventually(t, func() bool { return len(host.Snapshot().RecentEvents) == 1 }, time.Second, time.Millisecond)
	snapshot := host.Snapshot()
	require.EqualValues(t, 1, snapshot.Logs)
	require.EqualValues(t, 1, snapshot.Metrics["requests"])
	host.Close()
	_, err = host.Metric(ctx, &wire.HostMetricRequest{Capability: cap.ID, Name: "requests", Value: 1})
	require.Equal(t, codes.Unavailable, status.Code(err))
}

func TestPluginHostServicesReadAccountMetadataIsScoped(t *testing.T) {
	cap := hostCapability()
	cap.Permissions = append(cap.Permissions, pluginv2.PermissionAccountMetadata)
	installation := &PluginInstallation{ID: 9, Manifest: PluginManifest{Capabilities: []PluginCapability{cap}},
		Bindings: []PluginBinding{{ID: 4, Capability: cap.ID, Platform: PlatformOpenAI, AccountType: AccountTypeOAuth, Enabled: true, AccountIDs: []int64{7}}}}
	host := newPluginHostServices(installation, nil, func(context.Context, string, int64) ([]byte, error) {
		return []byte(`{"schema":1,"subscription":{"plan_type":"pro"}}`), nil
	})
	defer host.Close()
	response, err := host.ReadAccountMetadata(context.Background(), &wire.HostAccountMetadataRequest{Capability: cap.ID, AccountId: 7})
	require.NoError(t, err)
	require.True(t, response.Found)
	assert.JSONEq(t, `{"schema":1,"subscription":{"plan_type":"pro"}}`, string(response.MetadataJson))
	outside, err := host.ReadAccountMetadata(context.Background(), &wire.HostAccountMetadataRequest{Capability: cap.ID, AccountId: 8})
	require.NoError(t, err)
	assert.False(t, outside.Found)

	noPermission := hostCapability()
	without := newPluginHostServices(&PluginInstallation{ID: 10, Manifest: PluginManifest{Capabilities: []PluginCapability{noPermission}}, Bindings: installation.Bindings}, nil)
	defer without.Close()
	_, err = without.ReadAccountMetadata(context.Background(), &wire.HostAccountMetadataRequest{Capability: noPermission.ID, AccountId: 7})
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}
func TestPluginHostServicesRateLimitAndExpiredSecret(t *testing.T) {
	cap := hostCapability()
	host := newPluginHostServices(&PluginInstallation{ID: 1, Manifest: PluginManifest{Capabilities: []PluginCapability{cap}}},
		func(context.Context, string, string) (pluginv2.SecretValue, error) {
			return pluginv2.SecretValue{Value: []byte("expired"), ExpiresAt: time.Now().Add(-time.Second)}, nil
		})
	defer host.Close()
	_, err := host.ReadSecret(context.Background(), &wire.HostSecretRequest{Capability: cap.ID, Alias: "example"})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	host.mu.Lock()
	host.window = time.Now()
	host.calls = 100
	host.mu.Unlock()
	_, err = host.ReadConfig(context.Background(), &wire.HostCapabilityRequest{Capability: cap.ID})
	require.Equal(t, codes.ResourceExhausted, status.Code(err))
}

func TestPluginHostServicesSecretAuditRecordsOutcomesWithoutValue(t *testing.T) {
	cap := hostCapability()
	host := newPluginHostServices(&PluginInstallation{
		ID:       42,
		Manifest: PluginManifest{Capabilities: []PluginCapability{cap}},
		Bindings: []PluginBinding{{ID: 77, Capability: cap.ID, Enabled: true}},
	}, func(_ context.Context, _, alias string) (pluginv2.SecretValue, error) {
		switch alias {
		case "granted":
			return pluginv2.SecretValue{Value: []byte("secret-value"), ExpiresAt: time.Now().Add(time.Minute)}, nil
		case "expired":
			return pluginv2.SecretValue{Value: []byte("expired-value"), ExpiresAt: time.Now().Add(-time.Second)}, nil
		case "internal":
			return pluginv2.SecretValue{}, errors.New("provider failed with secret-value")
		default:
			return pluginv2.SecretValue{}, &PluginSecretReadError{Result: PluginSecretAuditDenied, ErrorCode: "secret_unavailable"}
		}
	})
	defer host.Close()
	host.setRuntimeMetadata("instance-42")
	ctx := WithPluginRequestProvenance(context.Background(), PluginRequestProvenance{CorrelationID: "corr-secret-1"})

	resp, err := host.ReadSecret(ctx, &wire.HostSecretRequest{Capability: cap.ID, Alias: "granted"})
	require.NoError(t, err)
	require.Equal(t, []byte("secret-value"), resp.Value)
	_, err = host.ReadSecret(ctx, &wire.HostSecretRequest{Capability: cap.ID, Alias: "unknown"})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	_, err = host.ReadSecret(ctx, &wire.HostSecretRequest{Capability: cap.ID, Alias: "expired"})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	_, err = host.ReadSecret(ctx, &wire.HostSecretRequest{Capability: cap.ID, Alias: "internal"})
	require.Equal(t, codes.PermissionDenied, status.Code(err))

	host.mu.Lock()
	host.window = time.Now()
	host.calls = 100
	host.mu.Unlock()
	_, err = host.ReadSecret(ctx, &wire.HostSecretRequest{Capability: cap.ID, Alias: "rate"})
	require.Equal(t, codes.ResourceExhausted, status.Code(err))

	snapshot := host.Snapshot()
	require.EqualValues(t, 1, snapshot.SecretReads[PluginSecretAuditGranted])
	require.EqualValues(t, 1, snapshot.SecretReads[PluginSecretAuditDenied])
	require.EqualValues(t, 1, snapshot.SecretReads[PluginSecretAuditExpired])
	require.EqualValues(t, 1, snapshot.SecretReads[PluginSecretAuditInternalError])
	require.EqualValues(t, 1, snapshot.SecretReads[PluginSecretAuditRateLimited])
	require.Len(t, snapshot.RecentSecretAudits, 5)
	for _, event := range snapshot.RecentSecretAudits {
		require.EqualValues(t, 42, event.PluginID)
		require.EqualValues(t, 77, event.BindingID)
		require.Equal(t, "instance-42", event.InstanceID)
		require.Equal(t, "corr-secret-1", event.CorrelationID)
		assert.NotContains(t, event.Alias, "secret-value")
		encoded, marshalErr := json.Marshal(event)
		require.NoError(t, marshalErr)
		assert.NotContains(t, string(encoded), "secret-value")
		assert.NotContains(t, string(encoded), "provider failed")
	}
}

func TestPluginHostServicesSecretAuditRateLimitsSameOutcome(t *testing.T) {
	cap := hostCapability()
	host := newPluginHostServices(&PluginInstallation{ID: 43, Manifest: PluginManifest{Capabilities: []PluginCapability{cap}}},
		func(context.Context, string, string) (pluginv2.SecretValue, error) {
			return pluginv2.SecretValue{}, &PluginSecretReadError{Result: PluginSecretAuditExpired, ErrorCode: "secret_expired"}
		})
	defer host.Close()
	request := &wire.HostSecretRequest{Capability: cap.ID, Alias: "same"}
	_, err := host.ReadSecret(context.Background(), request)
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	_, err = host.ReadSecret(context.Background(), request)
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	snapshot := host.Snapshot()
	require.EqualValues(t, 2, snapshot.SecretReads[PluginSecretAuditExpired])
	require.EqualValues(t, 1, snapshot.SecretAuditSuppressed)
	require.Len(t, snapshot.RecentSecretAudits, 1)
	assert.Equal(t, uint64(0), snapshot.RecentSecretAudits[0].Suppressed)
}

func TestPluginHostServicesSecretAuditSinkFailureIsBoundedAndRedacted(t *testing.T) {
	cap := hostCapability()
	received := make(chan PluginSecretAuditEvent, 1)
	host := newPluginHostServices(&PluginInstallation{ID: 44, Manifest: PluginManifest{Capabilities: []PluginCapability{cap}}},
		func(context.Context, string, string) (pluginv2.SecretValue, error) {
			return pluginv2.SecretValue{Value: []byte("sink-secret"), ExpiresAt: time.Now().Add(time.Minute)}, nil
		})
	defer host.Close()
	host.setSecretAuditSink(PluginSecretAuditSinkFunc(func(_ context.Context, event PluginSecretAuditEvent) error {
		received <- event
		return errors.New("sink failed with sink-secret")
	}))
	_, err := host.ReadSecret(context.Background(), &wire.HostSecretRequest{Capability: cap.ID, Alias: "sink"})
	require.NoError(t, err)
	select {
	case event := <-received:
		encoded, marshalErr := json.Marshal(event)
		require.NoError(t, marshalErr)
		assert.NotContains(t, string(encoded), "sink-secret")
	case <-time.After(time.Second):
		t.Fatal("secret audit sink was not invoked")
	}
	require.Eventually(t, func() bool { return host.Snapshot().SecretAuditSinkErrors == 1 }, time.Second, time.Millisecond*10)
}

func TestPluginHostEventQueueAppliesBackpressure(t *testing.T) {
	capability := pluginv2.CapabilityRequestPreprocess
	host := &pluginHostServices{permissions: map[string]map[pluginv2.Permission]bool{capability: {pluginv2.PermissionEventPublish: true}},
		events: make(chan PluginHostEvent, 1), stats: PluginHostSnapshot{Metrics: map[string]float64{}}}
	request := &wire.HostEventRequest{Capability: capability, Name: "policy.pass", Value: 1}
	_, err := host.PublishEvent(context.Background(), request)
	require.NoError(t, err)
	_, err = host.PublishEvent(context.Background(), request)
	require.Equal(t, codes.ResourceExhausted, status.Code(err))
	require.EqualValues(t, 1, host.Snapshot().EventsDropped)
	require.EqualValues(t, 1, host.Snapshot().EventsAccepted)
}
func TestPluginExtensionRPCErrorNeverDisclosesPluginText(t *testing.T) {
	source := status.Error(codes.InvalidArgument, "private-secret-in-plugin-message")
	err := safeExtensionRPCError("configuration failed", source)
	require.NotContains(t, err.Error(), "private-secret")
	require.Contains(t, err.Error(), "InvalidArgument")
	require.True(t, errors.Is(err, source))
}

func TestPluginSecretEnvelopeCannotMoveAcrossAliasOrPurpose(t *testing.T) {
	i := &PluginInstallation{ID: 1, Manifest: PluginManifest{SchemaVersion: 2, Capabilities: []PluginCapability{hostCapability()}}}
	repo := &extensionMemoryRepository{row: i}
	m := &PluginManager{repo: repo, encryptor: pluginTokenEncryptor{}}
	cap := pluginv2.CapabilityRequestPreprocess
	require.NoError(t, m.PutSecretGrant(context.Background(), 1, cap, "first", "private", 60))
	grant, err := repo.GetSecretGrant(context.Background(), 1, cap, "first")
	require.NoError(t, err)
	grant.Alias = "second"
	require.NoError(t, repo.PutSecretGrant(context.Background(), *grant))
	_, err = m.readPluginSecret(context.Background(), 1, cap, "second")
	require.Error(t, err)
	grant.EncryptedValue = "ENC:private-other-purpose"
	require.NoError(t, repo.PutSecretGrant(context.Background(), *grant))
	_, err = m.readPluginSecret(context.Background(), 1, cap, "second")
	require.Error(t, err)
}

// fakePluginKVStore 是内存版 PluginKVStore，按 (pluginKey, namespace, key) 三元组隔离，
// 用于确定性地验证宿主服务端的校验与命名空间隔离逻辑。
type fakePluginKVStore struct {
	mu     sync.Mutex
	values map[string][]byte
	ttls   map[string]time.Duration
}

func newFakePluginKVStore() *fakePluginKVStore {
	return &fakePluginKVStore{values: map[string][]byte{}, ttls: map[string]time.Duration{}}
}

func (f *fakePluginKVStore) compose(pluginKey, namespace, key string) string {
	return pluginKey + "\x00" + namespace + "\x00" + key
}

func (f *fakePluginKVStore) Get(_ context.Context, pluginKey, namespace, key string) ([]byte, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	value, ok := f.values[f.compose(pluginKey, namespace, key)]
	if !ok {
		return nil, false, nil
	}
	return append([]byte(nil), value...), true, nil
}

func (f *fakePluginKVStore) Set(_ context.Context, pluginKey, namespace, key string, value []byte, ttl time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	composed := f.compose(pluginKey, namespace, key)
	f.values[composed] = append([]byte(nil), value...)
	f.ttls[composed] = ttl
	return nil
}

func (f *fakePluginKVStore) Delete(_ context.Context, pluginKey, namespace, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	composed := f.compose(pluginKey, namespace, key)
	delete(f.values, composed)
	delete(f.ttls, composed)
	return nil
}

func (f *fakePluginKVStore) List(_ context.Context, pluginKey, namespace, keyPrefix string, limit int) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	prefix := f.compose(pluginKey, namespace, keyPrefix)
	scope := f.compose(pluginKey, namespace, "")
	var keys []string
	for composed := range f.values {
		if strings.HasPrefix(composed, prefix) {
			keys = append(keys, strings.TrimPrefix(composed, scope))
		}
	}
	sort.Strings(keys)
	if limit > 0 && len(keys) > limit {
		keys = keys[:limit]
	}
	return keys, nil
}

func TestPluginHostServiceServer_SetGetDeleteRoundtrip(t *testing.T) {
	store := newFakePluginKVStore()
	server := newPluginHostServiceServer("local.example.plugin", store, nil)
	ctx := context.Background()

	_, err := server.KVSet(ctx, &pluginv1.KVSetRequest{Namespace: "state", Key: "alpha", Value: []byte("v1"), TtlSeconds: 60})
	require.NoError(t, err)

	got, err := server.KVGet(ctx, &pluginv1.KVGetRequest{Namespace: "state", Key: "alpha"})
	require.NoError(t, err)
	assert.True(t, got.Found)
	assert.Equal(t, []byte("v1"), got.Value)
	assert.Equal(t, time.Minute, store.ttls[store.compose("local.example.plugin", "state", "alpha")])

	_, err = server.KVDelete(ctx, &pluginv1.KVDeleteRequest{Namespace: "state", Key: "alpha"})
	require.NoError(t, err)

	got, err = server.KVGet(ctx, &pluginv1.KVGetRequest{Namespace: "state", Key: "alpha"})
	require.NoError(t, err)
	assert.False(t, got.Found)
}

func TestPluginHostServiceServer_GetNotFound(t *testing.T) {
	server := newPluginHostServiceServer("local.example.plugin", newFakePluginKVStore(), nil)
	got, err := server.KVGet(context.Background(), &pluginv1.KVGetRequest{Namespace: "state", Key: "missing"})
	require.NoError(t, err)
	assert.False(t, got.Found)
	assert.Nil(t, got.Value)
}

func TestPluginHostServiceServer_NamespaceIsolationAcrossPlugins(t *testing.T) {
	store := newFakePluginKVStore()
	first := newPluginHostServiceServer("local.plugin.one", store, nil)
	second := newPluginHostServiceServer("local.plugin.two", store, nil)
	ctx := context.Background()

	_, err := first.KVSet(ctx, &pluginv1.KVSetRequest{Namespace: "state", Key: "shared", Value: []byte("first")})
	require.NoError(t, err)

	// 另一个插件即便使用完全相同的 namespace/key 也读不到，因为 pluginKey 由宿主注入。
	got, err := second.KVGet(ctx, &pluginv1.KVGetRequest{Namespace: "state", Key: "shared"})
	require.NoError(t, err)
	assert.False(t, got.Found)

	got, err = first.KVGet(ctx, &pluginv1.KVGetRequest{Namespace: "state", Key: "shared"})
	require.NoError(t, err)
	assert.True(t, got.Found)
	assert.Equal(t, []byte("first"), got.Value)
}

func TestPluginHostServiceServer_ListPrefixAndLimit(t *testing.T) {
	store := newFakePluginKVStore()
	server := newPluginHostServiceServer("local.example.plugin", store, nil)
	ctx := context.Background()
	for _, key := range []string{"account-1", "account-2", "account-3", "other-1"} {
		_, err := server.KVSet(ctx, &pluginv1.KVSetRequest{Namespace: "state", Key: key, Value: []byte("x")})
		require.NoError(t, err)
	}

	resp, err := server.KVList(ctx, &pluginv1.KVListRequest{Namespace: "state", KeyPrefix: "account-"})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"account-1", "account-2", "account-3"}, resp.Keys)

	resp, err = server.KVList(ctx, &pluginv1.KVListRequest{Namespace: "state", KeyPrefix: "account-", Limit: 2})
	require.NoError(t, err)
	assert.Len(t, resp.Keys, 2)
}

func TestPluginHostServiceServer_Validation(t *testing.T) {
	server := newPluginHostServiceServer("local.example.plugin", newFakePluginKVStore(), nil)
	ctx := context.Background()
	oversized := make([]byte, pluginKVMaxValueBytes+1)

	cases := []struct {
		name string
		call func() error
	}{
		{"empty namespace", func() error {
			_, err := server.KVGet(ctx, &pluginv1.KVGetRequest{Namespace: "", Key: "k"})
			return err
		}},
		{"namespace with colon", func() error {
			_, err := server.KVGet(ctx, &pluginv1.KVGetRequest{Namespace: "a:b", Key: "k"})
			return err
		}},
		{"key with glob", func() error {
			_, err := server.KVGet(ctx, &pluginv1.KVGetRequest{Namespace: "state", Key: "a*"})
			return err
		}},
		{"empty key", func() error {
			_, err := server.KVGet(ctx, &pluginv1.KVGetRequest{Namespace: "state", Key: ""})
			return err
		}},
		{"oversized value", func() error {
			_, err := server.KVSet(ctx, &pluginv1.KVSetRequest{Namespace: "state", Key: "k", Value: oversized})
			return err
		}},
		{"negative ttl", func() error {
			_, err := server.KVSet(ctx, &pluginv1.KVSetRequest{Namespace: "state", Key: "k", Value: []byte("v"), TtlSeconds: -1})
			return err
		}},
		{"ttl over max", func() error {
			_, err := server.KVSet(ctx, &pluginv1.KVSetRequest{Namespace: "state", Key: "k", Value: []byte("v"), TtlSeconds: int64(pluginKVMaxTTL/time.Second) + 1})
			return err
		}},
		{"ttl overflow", func() error {
			// 溢出用例：seconds*time.Second 会回绕成负值，必须仍判为超限而非放行。
			_, err := server.KVSet(ctx, &pluginv1.KVSetRequest{Namespace: "state", Key: "k", Value: []byte("v"), TtlSeconds: math.MaxInt64})
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			require.Error(t, err)
			assert.Equal(t, codes.InvalidArgument, status.Code(err))
		})
	}
}

func TestPluginHostServiceServer_UnavailableWhenNoStore(t *testing.T) {
	server := newPluginHostServiceServer("local.example.plugin", nil, nil)
	_, err := server.KVGet(context.Background(), &pluginv1.KVGetRequest{Namespace: "state", Key: "k"})
	require.Error(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(err))

	// pluginKey 为空同样视为不可用，避免键前缀塌缩导致的跨插件泄漏。
	blank := newPluginHostServiceServer("", newFakePluginKVStore(), nil)
	_, err = blank.KVGet(context.Background(), &pluginv1.KVGetRequest{Namespace: "state", Key: "k"})
	require.Error(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(err))

	// pluginKey 含 ':' 会破坏内部键结构，必须判定为不可用而非放行。
	tainted := newPluginHostServiceServer("bad:key", newFakePluginKVStore(), nil)
	_, err = tainted.KVGet(context.Background(), &pluginv1.KVGetRequest{Namespace: "state", Key: "k"})
	require.Error(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(err))
}

// pluginKey 的长度上限必须覆盖清单 id 的 maxLength（160），否则长 id 的合法插件会被
// 静默剥夺宿主服务能力。
func TestPluginHostServiceServer_PluginKeyLengthBoundary(t *testing.T) {
	ctx := context.Background()
	maxLen := newPluginHostServiceServer(strings.Repeat("a", pluginKVMaxPluginKeyLen), newFakePluginKVStore(), nil)
	_, err := maxLen.KVSet(ctx, &pluginv1.KVSetRequest{Namespace: "state", Key: "k", Value: []byte("v")})
	require.NoError(t, err)

	tooLong := newPluginHostServiceServer(strings.Repeat("a", pluginKVMaxPluginKeyLen+1), newFakePluginKVStore(), nil)
	_, err = tooLong.KVSet(ctx, &pluginv1.KVSetRequest{Namespace: "state", Key: "k", Value: []byte("v")})
	require.Error(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(err))
}

type fakeAccountDirectory struct {
	ids      []int64
	identity *PluginOutboundIdentity
	lastReq  int64
}

func (f *fakeAccountDirectory) ListPluginAccounts(_ context.Context, _, _ string) ([]int64, error) {
	return f.ids, nil
}

func (f *fakeAccountDirectory) ResolvePluginOutboundIdentity(_ context.Context, accountID int64) (*PluginOutboundIdentity, error) {
	f.lastReq = accountID
	if f.identity == nil || f.identity.AccountID != accountID {
		return nil, nil
	}
	return f.identity, nil
}

func TestPluginHostServiceServer_AccountDirectory(t *testing.T) {
	ctx := context.Background()
	dir := &fakeAccountDirectory{
		ids: []int64{3, 7},
		identity: &PluginOutboundIdentity{
			AccountID: 7, Platform: "openai", AccountType: "oauth", ProxyURL: "http://p:1",
			Token: "tok", Headers: http.Header{"Originator": {"codex-tui"}},
		},
	}
	scope := newPluginAccountScope(pluginAccountScopeEntry{Platform: "openai", AccountType: "oauth", AccountIDs: []int64{3, 7}})
	server := newPluginHostServiceServer("local.example.plugin", newFakePluginKVStore(), dir, scope)

	list, err := server.ListAccounts(ctx, &pluginv1.ListAccountsRequest{Platform: "openai", AccountType: "oauth"})
	require.NoError(t, err)
	assert.Equal(t, []int64{3, 7}, list.AccountIds)

	resolved, err := server.ResolveOutboundIdentity(ctx, &pluginv1.ResolveOutboundIdentityRequest{AccountId: 7})
	require.NoError(t, err)
	require.True(t, resolved.Found)
	assert.Equal(t, "tok", resolved.Token)
	assert.Equal(t, "http://p:1", resolved.ProxyUrl)
	assert.Equal(t, "codex-tui", resolved.Headers["Originator"].Values[0])

	// unknown account → found=false, no error
	miss, err := server.ResolveOutboundIdentity(ctx, &pluginv1.ResolveOutboundIdentityRequest{AccountId: 99})
	require.NoError(t, err)
	assert.False(t, miss.Found)

	// invalid account id → InvalidArgument
	_, err = server.ResolveOutboundIdentity(ctx, &pluginv1.ResolveOutboundIdentityRequest{AccountId: 0})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

// 无目录时账号目录 RPC 必须返回 Unavailable（KV 仍可用），保证未授权插件拿不到凭据。
func TestPluginHostServiceServer_DirectoryUnavailableWithoutDirectory(t *testing.T) {
	server := newPluginHostServiceServer("local.example.plugin", newFakePluginKVStore(), nil)
	_, err := server.ListAccounts(context.Background(), &pluginv1.ListAccountsRequest{})
	assert.Equal(t, codes.Unavailable, status.Code(err))
	_, err = server.ResolveOutboundIdentity(context.Background(), &pluginv1.ResolveOutboundIdentityRequest{AccountId: 1})
	assert.Equal(t, codes.Unavailable, status.Code(err))
	// KV 仍可用
	_, kvErr := server.KVGet(context.Background(), &pluginv1.KVGetRequest{Namespace: "state", Key: "k"})
	require.NoError(t, kvErr)
}

func TestPluginHostServiceServer_LegacyDirectoryCannotBypassEmptyScope(t *testing.T) {
	dir := &fakeAccountDirectory{ids: []int64{1, 2}, identity: &PluginOutboundIdentity{AccountID: 1, Token: "secret"}}
	server := newPluginHostServiceServer("local.example.plugin", newFakePluginKVStore(), dir)

	list, err := server.ListAccounts(context.Background(), &pluginv1.ListAccountsRequest{Platform: "openai", AccountType: "oauth"})
	require.NoError(t, err)
	assert.Empty(t, list.AccountIds)

	resolved, err := server.ResolveOutboundIdentity(context.Background(), &pluginv1.ResolveOutboundIdentityRequest{AccountId: 1})
	require.NoError(t, err)
	assert.False(t, resolved.Found)
}

func TestPluginDeclaresOpenAIOAuthCapability(t *testing.T) {
	match := PluginManifest{Capabilities: []PluginCapability{{ID: PluginCapabilityOpenAIOAuthOutbound, Platform: PlatformOpenAI, AccountType: AccountTypeOAuth}}}
	if !pluginDeclaresOpenAIOAuthCapability(match) {
		t.Fatal("matching capability must be recognized")
	}
	wrongType := PluginManifest{Capabilities: []PluginCapability{{ID: PluginCapabilityOpenAIOAuthOutbound, Platform: PlatformOpenAI, AccountType: "apikey"}}}
	if pluginDeclaresOpenAIOAuthCapability(wrongType) {
		t.Fatal("wrong account_type must not match")
	}
	empty := PluginManifest{}
	if pluginDeclaresOpenAIOAuthCapability(empty) {
		t.Fatal("no capability must not match")
	}
}

// buildHostServices 只对声明了 OpenAI OAuth 能力的插件注入账号目录。
func TestBuildHostServicesGatesDirectoryByCapability(t *testing.T) {
	dir := &fakeAccountDirectory{}
	m := &PluginManager{kvStore: newFakePluginKVStore(), accountDirectory: dir}

	authorized := &PluginInstallation{PluginKey: "p.authorized", Manifest: PluginManifest{
		Capabilities: []PluginCapability{{ID: PluginCapabilityOpenAIOAuthOutbound, Platform: PlatformOpenAI, AccountType: AccountTypeOAuth}},
	}}
	srv, ok := m.buildHostServices(authorized).(*pluginHostServiceServer)
	require.True(t, ok)
	require.NotNil(t, srv.directory)

	unauthorized := &PluginInstallation{PluginKey: "p.other", Manifest: PluginManifest{
		Capabilities: []PluginCapability{{ID: "some.other.capability", Platform: "x", AccountType: "y"}},
	}}
	srv2, ok := m.buildHostServices(unauthorized).(*pluginHostServiceServer)
	require.True(t, ok)
	require.Nil(t, srv2.directory, "unauthorized plugin must not receive the account directory")
	require.NotNil(t, srv2.store, "but KV remains available")
}
