package zrpc

import (
	"errors"

	"github.com/hunyxv/zrpc/codec"
	"github.com/hunyxv/zrpc/metadata"
)

type Request struct {
	Method   string
	Metadata metadata.MD
	Body     []byte
	Codec    codec.Codec
}

type Response struct {
	Metadata metadata.MD
	Body     []byte
	Codec    codec.Codec
}

func NewRequest(method string, value any, c codec.Codec) (*Request, error) {
	if method == "" {
		return nil, errors.New("zrpc: request method is required")
	}
	if c == nil {
		return nil, errors.New("zrpc: request codec is required")
	}
	body, err := c.Marshal(value)
	if err != nil {
		return nil, err
	}
	return &Request{Method: method, Metadata: metadata.New(), Body: body, Codec: c}, nil
}

func NewRequestBytes(method string, md metadata.MD, body []byte, c codec.Codec) (*Request, error) {
	if method == "" {
		return nil, errors.New("zrpc: request method is required")
	}
	if c == nil {
		return nil, errors.New("zrpc: request codec is required")
	}
	return &Request{
		Method:   method,
		Metadata: md.Copy(),
		Body:     append([]byte(nil), body...),
		Codec:    c,
	}, nil
}

func (r *Request) Decode(v any) error {
	if r == nil {
		return errors.New("zrpc: request is nil")
	}
	if r.Codec == nil {
		return errors.New("zrpc: request codec is required")
	}
	return r.Codec.Unmarshal(r.Body, v)
}

func NewResponse(value any, c codec.Codec) (*Response, error) {
	if c == nil {
		return nil, errors.New("zrpc: response codec is required")
	}
	body, err := c.Marshal(value)
	if err != nil {
		return nil, err
	}
	return &Response{Metadata: metadata.New(), Body: body, Codec: c}, nil
}

func NewResponseBytes(md metadata.MD, body []byte, c codec.Codec) (*Response, error) {
	if c == nil {
		return nil, errors.New("zrpc: response codec is required")
	}
	return &Response{
		Metadata: md.Copy(),
		Body:     append([]byte(nil), body...),
		Codec:    c,
	}, nil
}

func (r *Response) Decode(v any) error {
	if r == nil {
		return errors.New("zrpc: response is nil")
	}
	if r.Codec == nil {
		return errors.New("zrpc: response codec is required")
	}
	return r.Codec.Unmarshal(r.Body, v)
}
