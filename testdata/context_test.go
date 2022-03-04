package testdata

import (
	"context"
	"testing"

	"github.com/hunyxv/zrpc"
	"github.com/vmihailenco/msgpack/v5"
)

func TestMarshalUnmarshal(t *testing.T) {
	ctx := context.Background()
	ctx = context.WithValue(ctx, zrpc.PayloadKey, map[string]interface{}{"aaaaa": "bbbbb", "ccccc": 12345})
	ctx = zrpc.NewContext(ctx)
	data, err := msgpack.Marshal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(string(data))

	var ctx2 zrpc.Context
	if err := msgpack.Unmarshal(data, &ctx2); err != nil {
		t.Fatal(err)
	}
	t.Logf("%v", ctx2.Value(zrpc.PayloadKey))
}
