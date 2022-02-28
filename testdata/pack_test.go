package testdata

import (
	"testing"

	"github.com/hunyxv/zrpc"
)

type Item struct {
	Arg1 string `msgpack:"arg1"`
	Arg2 int    `msgpack:"arg2"`
}

func TestPackReq(t *testing.T) {

}

func TestPackResp(t *testing.T) {

}


func TestGetServerName(t *testing.T){
	t.Log(zrpc.DefaultNodeState.Node.ServiceName)
}