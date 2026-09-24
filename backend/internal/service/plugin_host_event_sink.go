package service

import (
	"context"
	"errors"
	"strings"
	"time"
)

// PluginHostEventSink is an optional cross-instance event port. The event is
// already host-validated and contains no request body, secret, or plugin error
// text. Implementations should persist or forward it without mutating it.
type PluginHostEventSink interface {
	RecordPluginHostEvent(context.Context, PluginHostEvent) error
}

// PluginHostMetricSink is the metric counterpart of PluginHostEventSink. A
// separate interface lets deployments persist only the stream they need.
type PluginHostMetricSink interface {
	RecordPluginHostMetric(context.Context, PluginHostMetric) error
}

type PluginHostEventSinkFunc func(context.Context, PluginHostEvent) error

func (f PluginHostEventSinkFunc) RecordPluginHostEvent(ctx context.Context, event PluginHostEvent) error {
	if f == nil {
		return nil
	}
	return f(ctx, event)
}

type PluginHostMetricSinkFunc func(context.Context, PluginHostMetric) error

func (f PluginHostMetricSinkFunc) RecordPluginHostMetric(ctx context.Context, metric PluginHostMetric) error {
	if f == nil {
		return nil
	}
	return f(ctx, metric)
}

// PluginHostMetric is a bounded, host-owned observation suitable for a
// cross-instance metrics sink. Value is finite and constrained by Host API
// validation before it reaches this type.
type PluginHostMetric struct {
	Time          time.Time `json:"time"`
	PluginID      int64     `json:"plugin_id"`
	Capability    string    `json:"capability"`
	Name          string    `json:"name"`
	Value         float64   `json:"value"`
	BindingID     int64     `json:"binding_id,omitempty"`
	InstanceID    string    `json:"instance_id,omitempty"`
	CorrelationID string    `json:"correlation_id,omitempty"`
}

const (
	pluginHostObservationQueueSize = 64
	pluginHostSinkTimeout          = 500 * time.Millisecond
	pluginHostMaxFieldRunes        = 128
)

func sanitizePluginHostObservationField(value string) string {
	value = strings.TrimSpace(strings.ToValidUTF8(value, ""))
	if value == "" || strings.ContainsAny(value, "\r\n\x00") {
		return ""
	}
	runes := []rune(value)
	if len(runes) > pluginHostMaxFieldRunes {
		runes = runes[:pluginHostMaxFieldRunes]
	}
	return string(runes)
}

type pluginHostObservationKind uint8

const (
	pluginHostObservationEvent pluginHostObservationKind = iota + 1
	pluginHostObservationMetric
)

type pluginHostObservation struct {
	kind   pluginHostObservationKind
	event  PluginHostEvent
	metric PluginHostMetric
}

var errPluginHostSinkPanic = errors.New("plugin host sink panic")

func pluginHostSinkErrorCode(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, errPluginHostSinkPanic) {
		return "sink_panic"
	}
	return "sink_error"
}
