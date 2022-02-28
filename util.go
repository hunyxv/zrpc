package zrpc

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"sync"
	"time"

	"github.com/hunyxv/utils/timer"
	"github.com/pborman/uuid"
)

var origin int64

func init() {
	start, err := time.ParseInLocation("2006-01-02 15:04:05", "2021-11-17 11:47:00", time.Local)
	if err != nil {
		panic(err)
	}
	origin = start.UnixNano() / int64(time.Millisecond)
}

func NewMessageID() (id string) {
	now := time.Now().UnixNano()/int64(time.Millisecond) - origin
	_uuid := uuid.NewRandom().Array()
	idPrefix := bytes.NewBuffer([]byte{})
	binary.Write(idPrefix, binary.BigEndian, now)
	var _id [27]byte
	hex.Encode(_id[:], idPrefix.Bytes()[3:])
	_id[10] = '-'
	hex.Encode(_id[11:], _uuid[8:])
	return string(_id[:])
}

type _Value struct {
	v interface{}
	t timer.TimerTask
}

type myMap struct {
	sync.Map
	timer *timer.HashedWheelTimer
}

func newMyMap(t *timer.HashedWheelTimer) *myMap {
	return &myMap{
		timer: t,
	}
}

func (m *myMap) Store(key interface{}, value interface{}) {
	v := &_Value{
		v: value,
		t: m.timer.Submit(5*time.Second, func() {
			m.Map.Delete(key)
		}),
	}
	m.Map.Store(key, v)
}

func (m *myMap) Load(key interface{}) (interface{}, bool) {
	v, ok := m.Map.Load(key)
	if !ok {
		return nil, false
	}
	value := v.(*_Value)
	value.t.Reset()
	return value.v, true
}

func (m *myMap) LoadAndDelete(key interface{}) (interface{}, bool) {
	v, ok := m.Map.LoadAndDelete(key)
	if !ok {
		return nil, false
	}
	value := v.(*_Value)
	value.t.Cancel()
	return value.v, true
}

func (m *myMap) Delete(key interface{}) {
	m.LoadAndDelete(key)
}

type rwchannel struct {
	ch        chan []byte
	buf       []byte
	blockSize int
}

func newRWChannel(size int) *rwchannel {
	if size <= 0 {
		size = 4096
	}
	return &rwchannel{
		ch:        make(chan []byte, 1),
		blockSize: size,
	}
}

func (rw *rwchannel) Read(b []byte) (n int, err error) {
	if len(rw.buf) > 0 {
		if len(b) <= len(rw.buf) {
			n = copy(b, rw.buf)
			rw.buf = rw.buf[n:]
			return
		}

		n = copy(b, rw.buf)
		rw.buf = rw.buf[:0]
	}

	data, ok := <-rw.ch
	if !ok {
		return n, io.EOF
	}
	c := copy(b[n:], data)
	n += c
	if c < len(data) {
		rw.buf = data[c:]
		return
	}
	return
}

func (rw *rwchannel) Write(b []byte) (n int, err error) {
	if len(b) <= rw.blockSize {
		select {
		case rw.ch <- b:
		default:
			return 0, errors.New("channle is closed")
		}
		return len(b), nil
	}

	for {
		tmp := make([]byte, rw.blockSize)
		c := copy(tmp, b)
		b = b[c:]
		select {
		case rw.ch <- b:
		default:
			return 0, errors.New("channle is closed")
		}
		n += c
		if c < rw.blockSize {
			return
		}
	}
}

func (rw *rwchannel) Close() error {
	close(rw.ch)
	return nil
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
