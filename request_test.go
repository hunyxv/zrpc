package zrpc

import (
	"testing"

	"github.com/hunyxv/zrpc/codec"
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
