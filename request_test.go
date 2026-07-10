package zrpc

import (
	"testing"

	"github.com/hunyxv/zrpc/codec"
	"github.com/hunyxv/zrpc/metadata"
)

type requestPayload struct {
	Name string `json:"name" msgpack:"name"`
}

func TestRequestDecodeAndResponseDecode(t *testing.T) {
	c := codec.Msgpack()
	req, err := NewRequest("user.Get", requestPayload{Name: "alice"}, c)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	if req.Method != "user.Get" {
		t.Fatalf("Request.Method = %q", req.Method)
	}

	var in requestPayload
	if err := req.Decode(&in); err != nil {
		t.Fatalf("Request.Decode() error = %v", err)
	}
	if in.Name != "alice" {
		t.Fatalf("request name = %q", in.Name)
	}

	resp, err := NewResponse(requestPayload{Name: "bob"}, c)
	if err != nil {
		t.Fatalf("NewResponse() error = %v", err)
	}
	var out requestPayload
	if err := resp.Decode(&out); err != nil {
		t.Fatalf("Response.Decode() error = %v", err)
	}
	if out.Name != "bob" {
		t.Fatalf("response name = %q", out.Name)
	}
}

func TestNewRequestRequiresMethod(t *testing.T) {
	_, err := NewRequest("", requestPayload{Name: "alice"}, codec.Msgpack())
	if err == nil {
		t.Fatal("NewRequest() error = nil, want non-nil")
	}
}

func TestNewRequestAndResponseRequireCodec(t *testing.T) {
	var c codec.Codec
	if _, err := NewRequest("user.Get", requestPayload{Name: "alice"}, c); err == nil {
		t.Fatal("NewRequest() error = nil, want non-nil")
	}
	if _, err := NewResponse(requestPayload{Name: "bob"}, c); err == nil {
		t.Fatal("NewResponse() error = nil, want non-nil")
	}
}

func TestNewRequestBytesCopiesAndDecodes(t *testing.T) {
	c := codec.Msgpack()
	raw, err := c.Marshal(requestPayload{Name: "alice"})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	md := metadata.New()
	md.Set("Trace-ID", "trace-1")

	req, err := NewRequestBytes("user.Get", md, raw, c)
	if err != nil {
		t.Fatalf("NewRequestBytes() error = %v", err)
	}
	md.Set("Trace-ID", "mutated")
	raw[0] ^= 0xff

	if got := req.Metadata.Get("trace-id"); got != "trace-1" {
		t.Fatalf("metadata = %q, want trace-1", got)
	}
	var out requestPayload
	if err := req.Decode(&out); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if out.Name != "alice" {
		t.Fatalf("Name = %q, want alice", out.Name)
	}
}

func TestNewResponseBytesCopiesAndDecodes(t *testing.T) {
	c := codec.Msgpack()
	raw, err := c.Marshal(requestPayload{Name: "bob"})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	md := metadata.New()
	md.Set("Trace-ID", "trace-2")

	resp, err := NewResponseBytes(md, raw, c)
	if err != nil {
		t.Fatalf("NewResponseBytes() error = %v", err)
	}
	md.Set("Trace-ID", "mutated")
	raw[0] ^= 0xff

	if got := resp.Metadata.Get("trace-id"); got != "trace-2" {
		t.Fatalf("metadata = %q, want trace-2", got)
	}
	var out requestPayload
	if err := resp.Decode(&out); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if out.Name != "bob" {
		t.Fatalf("Name = %q, want bob", out.Name)
	}
}

func TestNewRequestBytesRejectsInvalidInput(t *testing.T) {
	if _, err := NewRequestBytes("", nil, nil, codec.Msgpack()); err == nil {
		t.Fatal("NewRequestBytes() error = nil, want non-nil")
	}
	var c codec.Codec
	if _, err := NewRequestBytes("user.Get", nil, nil, c); err == nil {
		t.Fatal("NewRequestBytes() nil codec error = nil, want non-nil")
	}
	if _, err := NewResponseBytes(nil, nil, c); err == nil {
		t.Fatal("NewResponseBytes() nil codec error = nil, want non-nil")
	}
}
