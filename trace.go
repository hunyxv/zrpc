package zrpc

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type noopSpan struct {
	ctx    Context
	cancel context.CancelFunc
}

var _ trace.Span = noopSpan{}

// SpanContext returns an empty span context.
func (noopSpan) SpanContext() trace.SpanContext { return trace.SpanContext{} }

// IsRecording always returns false.
func (noopSpan) IsRecording() bool { return false }

// SetStatus does nothing.
func (noopSpan) SetStatus(codes.Code, string) {}

// SetError does nothing.
func (noopSpan) SetError(bool) {}

// SetAttributes does nothing.
func (noopSpan) SetAttributes(...attribute.KeyValue) {}

// End does nothing.
func (s noopSpan) End(...trace.SpanEndOption) {
	s.cancel()
}

// RecordError does nothing.
func (noopSpan) RecordError(error, ...trace.EventOption) {}

// AddEvent does nothing.
func (noopSpan) AddEvent(string, ...trace.EventOption) {}

// SetName does nothing.
func (noopSpan) SetName(string) {}

// TracerProvider returns a no-op TracerProvider
func (noopSpan) TracerProvider() trace.TracerProvider { return nil }
