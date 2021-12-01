package zrpc

import (
	"fmt"

	"github.com/vmihailenco/msgpack/v5"
)

const (
	PACKPATH = "__pack_path__" // pack 在集群中传播路径
	TTL      = "__ttl__"       // pack 在集群中传播跳数
)

type Header map[string][]string

func (h Header) Set(key, value string) {
	h[key] = []string{value}
}

func (h Header) Add(key, value string) {
	h[key] = append(h[key], value)
}

func (h Header) Get(key string) string {
	if len(h[key]) == 0 {
		return ""
	}
	return h[key][0]
}

func (h Header) Pop(key string) string {
	if _, ok := h[key]; ok {
		value := h[key][len(h[key])-1]
		h[key] = h[key][:len(h[key])-1]
		return value
	}
	return ""
}

func (h Header) Has(key string) bool {
	_, ok := h[key]
	return ok
}

type Pack struct {
	Identity   string               `msgpack:"identity"`
	MethodName string               `msgpack:"method"`
	Header     Header               `msgpack:"head"`
	Args       []msgpack.RawMessage `msgpack:"args"`
}

func (p *Pack) Set(key, value string) {
	if p.Header == nil {
		p.Header = make(Header)
	}
	p.Header.Set(key, value)
}

func (p *Pack) Get(key string) string {
	return p.Header.Get(key)
}

func (p *Pack) Marshal(args []interface{}) (pack []byte, err error) {
	if p.Header == nil || !p.Header.Has("message_id") {
		p.Set("message_id", NewMessageID())
	}

	if args != nil {
		for _, v := range args {
			arg, err := msgpack.Marshal(&v)
			if err != nil {
				return nil, fmt.Errorf("pack marshal args: %v", err)
			}
			p.Args = append(p.Args, arg)
		}
	}

	pack, err = msgpack.Marshal(&p)
	return
}

func (p *Pack) Unmarshal(b []byte) error {
	return msgpack.Unmarshal(b, &p)
}

type DefaultHeader struct {
	MsgID string `msgpack:"msgid"`
}
