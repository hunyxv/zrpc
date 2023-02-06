package client

import (
	"context"
	"crypto/md5"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"runtime"
	"sync"
	"time"

	"github.com/hunyxv/zrpc"
	zmq "github.com/pebbe/zmq4"
	"github.com/vmihailenco/msgpack/v5"
)

var (
	ErrClientConnectClosed = errors.New("client connect is closed")
	ErrNoServer            = errors.New("no service can be invoked")
)

type zConnecter interface {
	// Send 发送数据包
	Send(p *zrpc.Pack) (string, error)
	// SpecifySend 向指定服务发送数据包
	SpecifySend(id string, p *zrpc.Pack) error
	// Recv 接收数据包
	Recv() <-chan *zrpc.Pack
	// Close 关闭连接
	Close()
}

var _ zConnecter = (*directConnect)(nil)

type directConnect struct {
	ctx    context.Context
	cancel context.CancelFunc

	identity       string
	serverInfo     zrpc.Node
	svcSoc         *zrpc.Socket
	stateSoc       *zrpc.Socket
	expirationTime time.Time

	ch    chan *zrpc.Pack
	to    []byte
	mutex sync.Mutex
}

func newDirectConnect(mid string, server ServerInfo) (conn *directConnect, err error) {
	if len(mid) == 0 {
		return nil, errors.New("[zrpc-cli]: client identity cannot be empty")
	}

	var svcSoc, stateSoc *zrpc.Socket
	svcSoc, err = zrpc.NewSocket(mid, zmq.ROUTER, zrpc.Backend, "")
	if err != nil {
		return nil, err
	}

	stateSoc, err = zrpc.NewSocket(mid, zmq.SUB, zrpc.Frontend, "")
	if err != nil {
		svcSoc.Close()
		return
	}
	svcSoc.Connect(server.LocalEndpoint.String())
	stateSoc.Connect(server.StateEndpoint.String())
	stateSoc.Subscribe("")

	ctx, cancel := context.WithCancel(context.Background())
	conn = &directConnect{
		ctx:    ctx,
		cancel: cancel,

		identity: mid,
		svcSoc:   svcSoc,
		stateSoc: stateSoc,
		serverInfo: zrpc.Node{
			ServiceName:   server.ServerName,
			NodeID:        server.NodeID,
			LocalEndpoint: server.LocalEndpoint,
			StateEndpoint: server.StateEndpoint,
		},
		to: []byte(server.NodeID),
		ch: make(chan *zrpc.Pack, chanCap),
	}
	go conn.loop()
	return
}

func (dc *directConnect) loop() {
	hbPacket, _ := msgpack.Marshal(&zrpc.Pack{
		Header: zrpc.Header{zrpc.METHOD_NAME: []string{zrpc.HEARTBEAT}},
	})
	hbData := [][]byte{dc.to, hbPacket}

	hbInterval := time.Second * 5
	dc.expirationTime = time.Now().Add(hbInterval*2 + time.Second*2)
	tick := time.NewTicker(hbInterval)
	defer tick.Stop()

	for {
		select {
		case <-dc.ctx.Done():
			dc.Close()
			return
		case <-tick.C:
			dc.svcSoc.Send() <- hbData
			if time.Until(dc.expirationTime) < 0 {
				dc.reconnect()
				dc.expirationTime = time.Now().Add(hbInterval*2 + time.Second*2)
			}
		case data := <-dc.svcSoc.Recv():
			from := string(data[0])
			if from != dc.serverInfo.NodeID {
				continue
			}

			var p *zrpc.Pack
			err := msgpack.Unmarshal(data[1], &p)
			if err != nil {
				log.Printf("[zrpc-cli]: magpack.Unmarshal fail: %+v, data: %s", err, data[0])
				continue
			}

			switch p.MethodName() {
			case zrpc.HEARTBEAT:
				dc.expirationTime = time.Now().Add(hbInterval*2 + time.Second*2)
			case zrpc.DISCONNECT:
				dc.Close()
			default:
				dc.ch <- p
			}
		case statedata := <-dc.stateSoc.Recv():
			from := string(statedata[0])
			if from != dc.serverInfo.NodeID {
				continue
			}

			var node zrpc.Node
			msgpack.Unmarshal(statedata[1], &node)
			log.Printf("[zrpc-cli]: server node status changes: %v", node)
		}
	}
}

func (dc *directConnect) Send(p *zrpc.Pack) (string, error) {
	if dc.isClosed() {
		return "", ErrClientConnectClosed
	}

	p.Identity = dc.identity
	data, err := msgpack.Marshal(p)
	if err != nil {
		return "", err
	}

	dc.svcSoc.Send() <- [][]byte{[]byte(dc.serverInfo.NodeID), data}
	return dc.serverInfo.NodeID, nil
}
func (dc *directConnect) SpecifySend(id string, p *zrpc.Pack) (err error) {
	_, err = dc.Send(p)
	return
}
func (dc *directConnect) Recv() <-chan *zrpc.Pack { return dc.ch }

func (dc *directConnect) isClosed() bool {
	return dc.identity == ""
}

func (dc *directConnect) reconnect() {
	dc.mutex.Lock()
	defer dc.mutex.Unlock()

	dc.svcSoc.Disconnect(dc.serverInfo.LocalEndpoint.String())
	dc.stateSoc.Disconnect(dc.serverInfo.StateEndpoint.String())
	time.Sleep(100 * time.Millisecond)

	dc.svcSoc.Connect(dc.serverInfo.LocalEndpoint.String())
	dc.stateSoc.Connect(dc.serverInfo.StateEndpoint.String())
	log.Printf("[zrpc-cli]: reconnect endpoint: %s", dc.serverInfo.LocalEndpoint)
}

func (dc *directConnect) Close() {
	dc.mutex.Lock()
	defer dc.mutex.Unlock()
	if dc.isClosed() {
		return
	}

	dc.identity = ""
	dc.svcSoc.Disconnect(dc.serverInfo.LocalEndpoint.String())
	dc.stateSoc.Disconnect(dc.serverInfo.StateEndpoint.String())
	time.Sleep(100 * time.Millisecond)
	dc.stateSoc.Close()
	dc.svcSoc.Close()
	dc.cancel()

	dc.stateSoc = nil
	dc.svcSoc = nil
}

var _ zConnecter = (*connectManager)(nil)

type connectManager struct {
	ctx    context.Context
	cancel context.CancelFunc

	identity string
	svcSoc   *zrpc.Socket
	stateSoc *zrpc.Socket

	servicesMap   map[string]zrpc.Node // nodeid:node
	servicesExpir map[string]time.Time // nodeid:expiration
	services      []zrpc.Node
	hbInterval    time.Duration
	ch            chan *zrpc.Pack
	mutex         sync.RWMutex
}

func newConnectManager(mid string) (manager *connectManager, err error) {
	var svcSoc, stateSoc *zrpc.Socket
	svcSoc, err = zrpc.NewSocket(mid, zmq.ROUTER, zrpc.Backend, "")
	if err != nil {
		return nil, err
	}

	stateSoc, err = zrpc.NewSocket(mid, zmq.SUB, zrpc.Frontend, "")
	if err != nil {
		svcSoc.Close()
		return
	}
	stateSoc.Subscribe("")

	ctx, cancel := context.WithCancel(context.Background())
	manager = &connectManager{
		ctx:      ctx,
		cancel:   cancel,
		identity: mid,
		svcSoc:   svcSoc,
		stateSoc: stateSoc,

		servicesMap:   map[string]zrpc.Node{},
		servicesExpir: map[string]time.Time{},
		services:      []zrpc.Node{},
		hbInterval:    time.Second * 5,
		ch:            make(chan *zrpc.Pack, chanCap),
	}

	go manager.loop()
	return
}

func (manager *connectManager) loop() {
	hbPacket, _ := msgpack.Marshal(&zrpc.Pack{
		Header: zrpc.Header{zrpc.METHOD_NAME: []string{zrpc.HEARTBEAT}},
	})

	tick := time.NewTicker(manager.hbInterval)
	defer tick.Stop()

	for {
		select {
		case <-manager.ctx.Done():
			return
		case <-tick.C:
			go manager.sendHeartBeatPack(hbPacket)
		case data := <-manager.svcSoc.Recv():
			from := string(data[0])
			manager.mutex.RLock()
			_, ok := manager.servicesMap[from]
			if !ok {
				manager.mutex.RUnlock()
				continue
			}

			var p *zrpc.Pack
			err := msgpack.Unmarshal(data[1], &p)
			if err != nil {
				manager.mutex.RUnlock()
				log.Printf("[zrpc-cli]: magpack.Unmarshal fail: %+v, data: %s", err, data[0])
				continue
			}

			switch p.MethodName() {
			case zrpc.HEARTBEAT:
				manager.servicesExpir[from] = time.Now().Add(manager.hbInterval*2 + time.Second*2)
				manager.mutex.RUnlock()
			case zrpc.DISCONNECT:
				manager.mutex.RUnlock()
				manager.disconnect(from)
			default:
				manager.mutex.RUnlock()
				manager.ch <- p
			}
		}
	}
}

func (manager *connectManager) sendHeartBeatPack(p []byte) {
	manager.mutex.RLock()
	defer manager.mutex.RUnlock()

	for to := range manager.servicesMap {
		manager.svcSoc.Send() <- [][]byte{[]byte(to), p}
		if expir, ok := manager.servicesExpir[to]; ok {
			if time.Until(expir) < 0 {
				manager.reconnect(manager.servicesMap[to])
			}
		}
	}
}

func (manager *connectManager) reconnect(node zrpc.Node) {
	manager.svcSoc.Disconnect(node.LocalEndpoint.String())
	manager.stateSoc.Disconnect(node.StateEndpoint.String())
	time.Sleep(100 * time.Millisecond)

	manager.svcSoc.Connect(node.LocalEndpoint.String())
	manager.stateSoc.Connect(node.StateEndpoint.String())
	log.Printf("[zrpc-cli]: reconnect endpoint: %s", node.LocalEndpoint)
}

func (manager *connectManager) disconnect(nodeid string) {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()

	manager.svcSoc.Disconnect(nodeid)
	manager.stateSoc.Disconnect(nodeid)

	delete(manager.servicesMap, nodeid)
	delete(manager.servicesExpir, nodeid)
	for i, n := range manager.services {
		if n.NodeID == nodeid {
			manager.services[i] = manager.services[len(manager.services)-1]
			manager.services = manager.services[:len(manager.services)-1]
		}
	}
}

func (manager *connectManager) AddOrUpdate(nodeid string, metadata []byte) error {
	var node zrpc.Node
	if err := json.Unmarshal(metadata, &node); err != nil {
		return err
	}

	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	if manager.isClosed() {
		return nil
	}

	if n, ok := manager.servicesMap[nodeid]; ok {
		manager.svcSoc.Disconnect(n.LocalEndpoint.String())
		manager.stateSoc.Disconnect(n.StateEndpoint.String())
		time.Sleep(100 * time.Millisecond)
	}

	manager.svcSoc.Connect(node.LocalEndpoint.String())
	manager.svcSoc.Connect(node.StateEndpoint.String())
	manager.servicesMap[nodeid] = node
	var has bool
	for i, n := range manager.services {
		if n.NodeID == node.NodeID {
			manager.services[i] = node
			has = true
			break
		}
	}
	if !has {
		manager.services = append(manager.services, node)
	}

	manager.servicesExpir[node.NodeID] = time.Now().Add(manager.hbInterval*2 + time.Second*2)
	return nil
}

func (manager *connectManager) Delete(nodeid string) {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()

	if manager.isClosed() {
		return
	}

	if node, ok := manager.servicesMap[nodeid]; ok {
		manager.svcSoc.Disconnect(node.LocalEndpoint.String())
		manager.stateSoc.Disconnect(node.StateEndpoint.String())
		delete(manager.servicesMap, nodeid)
	}
	for i, node := range manager.services {
		if node.NodeID == nodeid {
			manager.services[i] = manager.services[len(manager.services)-1]
			manager.services = manager.services[:len(manager.services)-1]
			break
		}
	}
	delete(manager.servicesExpir, nodeid)
}

func (manager *connectManager) Send(p *zrpc.Pack) (string, error) {
	if manager.isClosed() {
		return "", ErrClientConnectClosed
	}
	if len(manager.services) == 0 {
		return "", ErrNoServer
	}
	msgid := p.Get(zrpc.MESSAGEID)
	if msgid == "" {
		msgid = zrpc.NewMessageID()
		p.Set(zrpc.MESSAGEID, msgid)
	}

	p.Identity = manager.identity
	data, err := msgpack.Marshal(p)
	if err != nil {
		return "", err
	}

	manager.mutex.RLock()
	defer manager.mutex.RUnlock()
	if len(manager.services) == 0 {
		return "", ErrNoServer
	}
	random := defaultHashKeyFunc(msgid)
	i := random % len(manager.services)
	node := manager.services[i]
	manager.svcSoc.Send() <- [][]byte{[]byte(node.NodeID), data}
	return node.NodeID, nil
}

func (manager *connectManager) SpecifySend(id string, p *zrpc.Pack) error {
	manager.mutex.RLock()
	defer manager.mutex.RUnlock()

	if manager.isClosed() {
		return ErrClientConnectClosed
	}

	node, ok := manager.servicesMap[id]
	if !ok {
		return fmt.Errorf("[zrpc-cli]: no server with id: %s", id)
	}

	msgid := p.Get(zrpc.MESSAGEID)
	if msgid == "" {
		msgid = zrpc.NewMessageID()
		p.Set(zrpc.MESSAGEID, msgid)
	}

	p.Identity = manager.identity
	data, err := msgpack.Marshal(p)
	if err != nil {
		return err
	}

	manager.svcSoc.Send() <- [][]byte{[]byte(node.NodeID), data}
	return nil
}
func (manager *connectManager) Recv() <-chan *zrpc.Pack { return manager.ch }

func (manager *connectManager) isClosed() bool {
	return manager.identity == ""
}
func (manager *connectManager) Close() {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()

	if manager.isClosed() {
		return
	}

	manager.identity = ""

	for _, node := range manager.services {
		manager.svcSoc.Disconnect(node.LocalEndpoint.String())
		manager.stateSoc.Disconnect(node.StateEndpoint.String())
		delete(manager.servicesMap, node.NodeID)
		delete(manager.servicesExpir, node.NodeID)
	}
	manager.services = nil
	manager.servicesExpir = nil
	time.Sleep(100 * time.Millisecond)
	manager.svcSoc.Close()
	manager.stateSoc.Close()
	manager.cancel()

	manager.stateSoc = nil
	manager.svcSoc = nil
}

var chanCap = func() int {
	if runtime.GOMAXPROCS(0) == 1 {
		return 0
	}
	return 1
}()

func defaultHashKeyFunc(key string) int {
	hashkey := md5.Sum([]byte(key))
	return int(binary.BigEndian.Uint16(hashkey[14:]))
}
