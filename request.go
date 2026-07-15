package zrpc

import (
	"errors"

	"github.com/hunyxv/zrpc/codec"
	"github.com/hunyxv/zrpc/metadata"
)

// Request 表示一次 unary RPC 的已编码请求。
type Request struct {
	// Method 是完整 RPC 方法名，例如 "hello.Say"。
	Method string
	// Metadata 保存随请求传递的元数据。
	Metadata metadata.MD
	// Body 是通过 Codec 编码后的请求体。
	Body []byte
	// Codec 用于将 Body 解码回业务请求结构体。
	Codec codec.Codec
}

// Response 表示一次 unary RPC 的已编码响应。
type Response struct {
	// Metadata 保存随响应返回的元数据。
	Metadata metadata.MD
	// Body 是通过 Codec 编码后的响应体。
	Body []byte
	// Codec 用于将 Body 解码回业务响应结构体。
	Codec codec.Codec
}

// NewRequest 将业务请求值编码为 Request。
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

// NewRequestBytes 使用已经编码好的 body 构造 Request，并复制 metadata 和 body。
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

// Decode 将 Request.Body 解码到 v。
func (r *Request) Decode(v any) error {
	if r == nil {
		return errors.New("zrpc: request is nil")
	}
	if r.Codec == nil {
		return errors.New("zrpc: request codec is required")
	}
	return r.Codec.Unmarshal(r.Body, v)
}

// NewResponse 将业务响应值编码为 Response。
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

// NewResponseBytes 使用已经编码好的 body 构造 Response，并复制 metadata 和 body。
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

// Decode 将 Response.Body 解码到 v。
func (r *Response) Decode(v any) error {
	if r == nil {
		return errors.New("zrpc: response is nil")
	}
	if r.Codec == nil {
		return errors.New("zrpc: response codec is required")
	}
	return r.Codec.Unmarshal(r.Body, v)
}
