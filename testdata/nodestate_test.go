package testdata

import (
	"encoding/json"
	"testing"

	"github.com/hunyxv/zrpc"
)

func TestNodeState(t *testing.T) {
	state := &zrpc.NodeState{
		Node: &zrpc.Node{
			ServiceName: "servicename",
			NodeID:      "abc",
			IsIdle:      true,
		},
	}

	b := state.Marshal()
	t.Log(string(b), len(b))

	var state2 = &zrpc.NodeState{}
	state2.Unmarshal(b)
	t.Log(state2.Node)

	str, _ := json.Marshal(state.Node)
	t.Log(string(str), len(str))
}
