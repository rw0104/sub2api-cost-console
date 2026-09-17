package service

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	pluginv2 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2"
	"github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2/wire"
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
