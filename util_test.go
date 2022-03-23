package zrpc

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/hunyxv/utils/timer"
)

func TestMyMap(t *testing.T) {
	timerWheel, _ := timer.NewHashedWheelTimer(context.Background())
	go timerWheel.Start()
	defer timerWheel.Stop()
	m := newMyMap(timerWheel, 10*time.Second)
	m.Store("testkey1", "aaaaaa")

	v, ok := m.Load("testkey")
	if !ok {
		t.Fatal("key is not exists")
	}
	t.Log(v)
	time.Sleep(3 * time.Second)
	_, ok = m.Load("testkey")
	if !ok {
		t.Fatal("key is not exists")
	}
	time.Sleep(5 * time.Second)
	_, ok = m.Load("testkey")
	if ok {
		t.Fatal("key is not deleted")
	}
}

func TestMessageID(t *testing.T) {
	now := time.Now()
	id := NewMessageID()
	cost := time.Since(now)
	t.Log(id, cost)
}

func BenchmarkMessageID(b *testing.B) {
	var m sync.Map

	b.RunParallel(func(p *testing.PB) {
		for p.Next() {
			id := NewMessageID()
			if _, loaded := m.LoadOrStore(id, struct{}{}); loaded {
				b.Fatal()
			}
		}
	})
}

func TestGetMarshalNode(t *testing.T) {
	node := DefaultNode
	data, err := json.Marshal(node)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(data)

	var node2 Node
	err = json.Unmarshal(data, &node2)
	t.Log(node2)
}
