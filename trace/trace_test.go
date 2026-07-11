package trace

import (
	"context"
	"testing"

	"github.com/hunyxv/zrpc/metadata"
)

func TestInjectExtractNoop(t *testing.T) {
	md := metadata.New()
	Inject(context.Background(), md)
	ctx := Extract(context.Background(), md)
	if ctx == nil {
		t.Fatal("Extract returned nil context")
	}
}

func TestCarrierKeys(t *testing.T) {
	md := metadata.New()
	md.Set("Traceparent", "value")
	keys := carrier{md: md}.Keys()
	if len(keys) != 1 || keys[0] != "traceparent" {
		t.Fatalf("keys = %#v", keys)
	}
}
