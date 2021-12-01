package testdata

import (
	"sync"
	"testing"
	"time"

	"github.com/hunyxv/zrpc"
)

func TestMessageID(t *testing.T) {
	now := time.Now()
	id := zrpc.NewMessageID()
	cost := time.Since(now)
	t.Log(id, cost)
}

func BenchmarkMessageID(b *testing.B) {
	var m sync.Map

	b.RunParallel(func(p *testing.PB) {
		for p.Next() {
			id := zrpc.NewMessageID()
			if _, loaded := m.LoadOrStore(id, struct{}{}); loaded {
				b.Fatal()
			}
		}
	})
}
