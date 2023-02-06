package zrpc

import (
	"bytes"
	"container/heap"
	"encoding/binary"
	"encoding/hex"
	"io"
	"math/rand"
	"net"
	"os"
	"path"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hunyxv/utils/timer"
	"github.com/pborman/uuid"
)

var origin int64

func init() {
	start, err := time.ParseInLocation("2006-01-02 15:04:05", "2022-03-03 12:00:00", time.Local)
	if err != nil {
		panic(err)
	}
	origin = start.UnixNano() / int64(time.Millisecond)
}

// NewMessageID 生成消息ID，前5字节是时间戳(ms)，后11字节是随机数
func NewMessageID() (id string) {
	now := time.Now().UnixMilli() - origin
	idPrefix := bytes.NewBuffer([]byte{})
	binary.Write(idPrefix, binary.BigEndian, now)
	var _id = make([]byte, 32)
	hex.Encode(_id[:], idPrefix.Bytes()[3:])
	random := make([]byte, 11)
	if _, err := rand.Read(random); err != nil {
		_uuid := uuid.NewRandom().Array()
		random = _uuid[5:]
	}
	hex.Encode(_id[10:], random)
	return string(_id)
}

type _Value struct {
	v any
	t timer.TimerTask
}

type myMap struct {
	sync.Map
	timeoutPeriod time.Duration
	timer         *timer.HashedWheelTimer
}

func newMyMap(t *timer.HashedWheelTimer, timeout time.Duration) *myMap {
	return &myMap{
		timeoutPeriod: timeout * 3,
		timer:         t,
	}
}

func (m *myMap) Store(key any, value any) {
	v := &_Value{
		v: value,
		t: m.timer.Submit(m.timeoutPeriod, func() {
			m.Map.Delete(key)
		}),
	}
	m.Map.Store(key, v)
}

func (m *myMap) Load(key any) (any, bool) {
	v, ok := m.Map.Load(key)
	if !ok {
		return nil, false
	}
	value := v.(*_Value)
	value.t.Reset()
	return value.v, true
}

func (m *myMap) LoadAndDelete(key any) (any, bool) {
	v, ok := m.Map.LoadAndDelete(key)
	if !ok {
		return nil, false
	}
	value := v.(*_Value)
	value.t.Cancel()
	return value.v, true
}

func (m *myMap) Delete(key any) {
	m.LoadAndDelete(key)
}

type activeMethodFuncs struct {
	sync.Map
	timeoutPeriod time.Duration
	timer         *timer.HashedWheelTimer
}

func newActiveMethodFuncs(t *timer.HashedWheelTimer, timeout time.Duration) *activeMethodFuncs {
	return &activeMethodFuncs{
		timeoutPeriod: timeout,
		timer:         t,
	}
}

func (m *activeMethodFuncs) Store(key any, value any) {
	v := &_Value{
		v: value,
		t: m.timer.Submit(m.timeoutPeriod, func() {
			if value, ok := m.Map.LoadAndDelete(key); ok {
				v := value.(*_Value)
				if f, ok := v.v.(methodFunc); ok {
					f.Release()
				}
			}
		}),
	}
	m.Map.Store(key, v)
}

func (m *activeMethodFuncs) Load(key any) (any, bool) {
	v, ok := m.Map.Load(key)
	if !ok {
		return nil, false
	}
	value := v.(*_Value)
	value.t.Reset()
	return value.v, true
}

func (m *activeMethodFuncs) LoadAndDelete(key any) (any, bool) {
	v, ok := m.Map.LoadAndDelete(key)
	if !ok {
		return nil, false
	}
	value := v.(*_Value)
	value.t.Cancel()
	return value.v, true
}

func (m *activeMethodFuncs) Delete(key any) {
	m.LoadAndDelete(key)
}

func getServerName() string {
	pwd, _ := os.Getwd()
	_, name := path.Split(pwd)
	return name
}

func dropCR(data []byte) []byte {
	if len(data) > 0 && data[len(data)-1] == '\r' {
		return data[0 : len(data)-1]
	}
	return data
}

func ScanLines(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}

	i := bytes.IndexByte(data, '\n')
	if i > 0 {
		if data[i-1] == '\r' {
			return i + 1, dropCR(data[0:i]), nil
		}
	}

	// If we're at EOF, we have a final, non-terminated line. Return it.
	if atEOF {
		return len(data), dropCR(data), nil
	}
	// Request more data.
	return 0, nil, nil
}

func isNil(i any) bool {
	vi := reflect.ValueOf(i)
	if vi.Kind() == reflect.Ptr {
		return vi.IsNil()
	}
	return i == nil
}

// getLocalIps 获取本机ip地址（ipv4）
func getLocalIps() ([]string, error) {
	interfaceAddr, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}

	ips := []string{}
	for _, addr := range interfaceAddr {
		ipNet, isVailIpNet := addr.(*net.IPNet)
		if isVailIpNet && !ipNet.IP.IsLoopback() {
			if ipNet.IP.To4() != nil {
				ips = append(ips, ipNet.IP.String())
			}
		}
	}
	return ips, nil
}

// heapQueue 有序队列
type HeapQueue struct {
	cursor uint64
	get    chan *Pack
	queue  []*Pack
	mutex  sync.Mutex
	buf []byte
}

func NewHeapQueue() *HeapQueue {
	return &HeapQueue{
		get:   make(chan *Pack, 1),
		queue: make([]*Pack, 0),
	}
}

func (hq *HeapQueue) Len() int { return len(hq.queue) }

func (hq *HeapQueue) Less(i, j int) bool { return hq.queue[i].SequenceID < hq.queue[j].SequenceID }

func (hq *HeapQueue) Swap(i, j int) { hq.queue[i], hq.queue[j] = hq.queue[j], hq.queue[i] }

func (hq *HeapQueue) Push(x any) { hq.queue = append(hq.queue, x.(*Pack)) }

func (hq *HeapQueue) Pop() any {
	l := len(hq.queue)
	if l == 0 {
		return nil
	}

	item := hq.queue[l-1]
	hq.queue = hq.queue[:l-1]
	return item
}

func (hq *HeapQueue) Insert(p *Pack) {
	hq.mutex.Lock()
	defer hq.mutex.Unlock()
	if atomic.LoadUint64(&(hq.cursor)) == p.SequenceID {
		hq.get <- p
		return
	}

	heap.Push(hq, p)
}

func (hq *HeapQueue) Get() (*Pack, error) {
	hq.mutex.Lock()
	if l := len(hq.queue); l > 0 {
		pack := hq.queue[0]
		if pack.SequenceID == atomic.LoadUint64(&(hq.cursor)) {
			heap.Pop(hq)
			atomic.AddUint64(&(hq.cursor), 1)
			hq.mutex.Unlock()
			return pack, nil
		}
	}
	hq.mutex.Unlock()

	for pack := range hq.get {
		hq.mutex.Lock()
		atomic.AddUint64(&(hq.cursor), 1)
		hq.mutex.Unlock()
		return pack, nil
	}

	return nil, io.EOF
}

func (hq *HeapQueue) Release() {
	close(hq.get)
}


func (hq *HeapQueue) Read(b []byte) (int, error) {
	if len(hq.buf) > 0 {
		n := copy(b, hq.buf)
		hq.buf = hq.buf[n:]
		return n, nil
	}

	pack, err := hq.Get()
	if err != nil {
		return 0, err
	}

	n := copy(b, pack.Args[0])
	hq.buf = pack.Args[0][n:]
	return n, nil
}
