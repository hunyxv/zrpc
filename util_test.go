package zrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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

func TestHeapQueue(t *testing.T) {
	queue := NewHeapQueue()
	go func() {
		queue.Insert(&Pack{
			SequenceID: 2,
			Args:       [][]byte{{1}},
		})
		time.Sleep(100 * time.Millisecond)
		queue.Insert(&Pack{
			SequenceID: 1,
			Args:       [][]byte{{2}},
		})
		time.Sleep(200 * time.Millisecond)
		queue.Insert(&Pack{
			SequenceID: 3,
			Args:       [][]byte{{3}},
		})
		time.Sleep(300 * time.Millisecond)
		queue.Insert(&Pack{
			SequenceID: 5,
			Args:       [][]byte{{4}},
		})
		queue.Insert(&Pack{
			SequenceID: 0,
			Args:       [][]byte{{6}},
		})
		queue.Release()
		queue.Insert(&Pack{
			SequenceID: 4,
			Args:       [][]byte{{5}},
		})
	}()

	for {
		b, err := queue.Get()
		if err != nil {
			if err == io.EOF {
				t.Log(err)
				break
			}
			t.Fatal(err)
		}
		t.Log(b)
	}
}

func TestHQReader(t *testing.T) {
	queue := NewHeapQueue()
	go func() {
		queue.Insert(&Pack{
			SequenceID: 2,
			Args:       [][]byte{{1,1,1,1,1,1,1,1,1,1}},
		})
		time.Sleep(100 * time.Millisecond)
		queue.Insert(&Pack{
			SequenceID: 1,
			Args:       [][]byte{{2,2,2,2,2,2,2,2,2,2}},
		})
		time.Sleep(200 * time.Millisecond)
		queue.Insert(&Pack{
			SequenceID: 3,
			Args:       [][]byte{{3,3,3,3,3,3,3,3,3,3}},
		})
		time.Sleep(300 * time.Millisecond)
		queue.Insert(&Pack{
			SequenceID: 5,
			Args:       [][]byte{{4,4,4,4,4,4,4,4,4,4}},
		})
		queue.Insert(&Pack{
			SequenceID: 0,
			Args:       [][]byte{{6,6,6,6,6,6,6,6,6,6}},
		})
		queue.Release()
		queue.Insert(&Pack{
			SequenceID: 4,
			Args:       [][]byte{{5,5,5,5,5,5,5}},
		})
	}()
	io.Pipe()
	b := make([]byte, 11)
	var n int 
	var err error
	for n, err = queue.Read(b); err == nil; n, err = queue.Read(b) {
		fmt.Println(b[:n])
	}
	t.Log(err)
}