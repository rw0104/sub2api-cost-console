package service

import (
	"context"
	"errors"
	"testing"
	"time"

	pluginv2 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2"
	"github.com/Wei-Shaw/sub2api/pkg/pluginapi/v2/wire"
	"github.com/stretchr/testify/require"
)

func TestPluginHostObservationSinksReceiveCorrelatedBoundedRecords(t *testing.T) {
	capability := hostCapability()
	host := newPluginHostServices(&PluginInstallation{
		ID:       90,
		Manifest: PluginManifest{Capabilities: []PluginCapability{capability}},
		Bindings: []PluginBinding{{ID: 91, Capability: capability.ID, Enabled: true}},
	}, nil)
	defer host.Close()
	host.setRuntimeMetadata("instance-90")
	events := make(chan PluginHostEvent, 1)
	metrics := make(chan PluginHostMetric, 1)
	host.setEventSink(PluginHostEventSinkFunc(func(_ context.Context, event PluginHostEvent) error {
		events <- event
		return nil
	}))
	host.setMetricSink(PluginHostMetricSinkFunc(func(_ context.Context, metric PluginHostMetric) error {
		metrics <- metric
		return nil
	}))
	ctx := WithPluginRequestProvenance(context.Background(), PluginRequestProvenance{CorrelationID: "corr-90"})

	_, err := host.PublishEvent(ctx, &wire.HostEventRequest{Capability: capability.ID, Name: "policy.pass", Value: 3})
	require.NoError(t, err)
	_, err = host.Metric(ctx, &wire.HostMetricRequest{Capability: capability.ID, Name: "requests", Value: 3})
	require.NoError(t, err)

	select {
	case event := <-events:
		require.Equal(t, int64(90), event.PluginID)
		require.Equal(t, int64(91), event.BindingID)
		require.Equal(t, "instance-90", event.InstanceID)
		require.Equal(t, "corr-90", event.CorrelationID)
	case <-time.After(time.Second):
		t.Fatal("event sink was not invoked")
	}
	select {
	case metric := <-metrics:
		require.Equal(t, int64(90), metric.PluginID)
		require.Equal(t, int64(91), metric.BindingID)
		require.Equal(t, "instance-90", metric.InstanceID)
		require.Equal(t, "corr-90", metric.CorrelationID)
		require.Equal(t, float64(3), metric.Value)
	case <-time.After(time.Second):
		t.Fatal("metric sink was not invoked")
	}
}

func TestPluginHostObservationSinkIsBoundedAndFailureIsAsync(t *testing.T) {
	capability := PluginCapability{ID: pluginv2.CapabilityRequestPreprocess, Platform: PlatformOpenAI, AccountType: AccountTypeOAuth}
	host := newPluginHostServices(&PluginInstallation{
		ID:       92,
		Manifest: PluginManifest{Capabilities: []PluginCapability{capability}},
	}, nil)
	defer host.Close()
	started := make(chan struct{})
	release := make(chan struct{})
	host.setEventSink(PluginHostEventSinkFunc(func(ctx context.Context, _ PluginHostEvent) error {
		select {
		case <-started:
		default:
			close(started)
		}
		select {
		case <-release:
			return errors.New("sink failed")
		case <-ctx.Done():
			return ctx.Err()
		}
	}))

	host.enqueueObservation(pluginHostObservation{kind: pluginHostObservationEvent, event: PluginHostEvent{PluginID: 92, Name: "policy.pass"}})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("sink worker did not start")
	}
	for index := 0; index < pluginHostObservationQueueSize*3; index++ {
		host.enqueueObservation(pluginHostObservation{kind: pluginHostObservationEvent, event: PluginHostEvent{PluginID: 92, Name: "policy.pass"}})
	}
	require.Eventually(t, func() bool { return host.Snapshot().EventSinkDropped > 0 }, time.Second, time.Millisecond)

	close(release)
	require.Eventually(t, func() bool { return host.Snapshot().EventSinkErrors > 0 }, time.Second, time.Millisecond*10)
}
