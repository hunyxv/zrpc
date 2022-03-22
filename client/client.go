package client

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"

	"github.com/hunyxv/zrpc"
	"github.com/pborman/uuid"
)

var (
	// ErrClosed client is closed
	ErrClosed = errors.New("zrpc-cli: client is closed")
)

type client interface {
	packSender
	insertNewChannel(methodName string, ch methodChannel)
	removeChannel(methodName string, msgid string)
}

var _ client = (*ZrpcClient)(nil)

type ZrpcClient struct {
	identity         string
	zconn            zConnecter
	channels         map[string]*sync.Map
	serviceDiscovery zrpc.ServiceDiscover
	services         sync.Map
	mutex            sync.RWMutex
	_closed          chan struct{}
}

// NewDirectClient 根据 rpc 服务地址连接的客户端（只适用于单节点服务）
func NewDirectClient(server ServerInfo) (*ZrpcClient, error) {
	identity := "cli-" + uuid.NewRandom().String()
	conn, err := newDirectConnect(identity, server)
	if err != nil {
		return nil, err
	}
	client := &ZrpcClient{
		identity: identity,
		zconn:    conn,
		channels: make(map[string]*sync.Map),
		_closed:  make(chan struct{}),
	}
	go func() {
		for {
			select {
			case <-client._closed:
				return
			case p := <-client.zconn.Recv():
				client.mutex.RLock()
				chs, ok := client.channels[p.MethodName()]
				if !ok {
					log.Printf("[zrpc-cli]: returned result cannot find methodfunc")
					continue
				}
				client.mutex.RUnlock()
				msgid := p.Get(zrpc.MESSAGEID)
				ch, ok := chs.Load(msgid)
				if !ok {
					log.Printf("[zrpc-cli]: returned result cannot find consumer")
					continue
				}

				ch.(methodChannel).Receive(p)
			}
		}
	}()

	return client, nil
}

// NewClient 使用注册/发现中心创建客户端
func NewClient(discover zrpc.ServiceDiscover) (*ZrpcClient, error) {
	identity := "cli-" + uuid.NewRandom().String()
	conn, err := newConnectManager(identity)
	if err != nil {
		return nil, err
	}
	client := &ZrpcClient{
		identity:         identity,
		zconn:            conn,
		channels:         make(map[string]*sync.Map),
		serviceDiscovery: discover,
		_closed:          make(chan struct{}),
	}
	go client.serviceDiscovery.Watch(conn)
	go func() {
		for {
			select {
			case <-client._closed:
				return
			case p := <-client.zconn.Recv():
				client.mutex.RLock()
				chs, ok := client.channels[p.MethodName()]
				if !ok {
					log.Printf("[zrpc-cli]: returned result cannot find methodfunc")
					continue
				}
				client.mutex.RUnlock()
				msgid := p.Get(zrpc.MESSAGEID)
				ch, ok := chs.Load(msgid)
				if !ok {
					log.Printf("[zrpc-cli]: returned result cannot find consumer")
					continue
				}

				ch.(methodChannel).Receive(p)
			}
		}
	}()

	return client, nil
}

func (cli *ZrpcClient) Identity() string {
	return cli.identity
}

func (cli *ZrpcClient) isClosed() bool {
	select {
	case <-cli._closed:
		return true
	default:
		return false
	}
}

// Decorator 将 server proxy 装饰为可调用 server
func (cli *ZrpcClient) Decorator(name string, i interface{}, retry int) error {
	if cli.isClosed() {
		return ErrClosed
	}

	_, ok := cli.services.LoadOrStore(name, i)
	if ok {
		return fmt.Errorf("service [%s] is exists", name)
	}

	proxy := newInstanceProxy(name, i, cli)
	err := proxy.init()
	if err != nil {
		cli.services.Delete(name)
		return err
	}

	return nil
}

func (cli *ZrpcClient) get(context.Context) (client, error) {
	if cli.isClosed() {
		return nil, ErrClosed
	}
	return cli, nil
}

func (cli *ZrpcClient) put(client) {}

// GetSerivce 获取已注册 rpc 服务实例以供使用
func (cli *ZrpcClient) GetSerivce(name string) (interface{}, bool) {
	if cli.isClosed() {
		return nil, false
	}
	return cli.services.Load(name)
}

func (cli *ZrpcClient) getChannels(methodName string) *sync.Map {
	cli.mutex.RLock()
	chs, ok := cli.channels[methodName]
	if !ok {
		cli.mutex.RUnlock()
		cli.mutex.Lock()
		chs, ok = cli.channels[methodName]
		if !ok {
			chs = new(sync.Map)
			cli.channels[methodName] = chs
		}
		cli.mutex.Unlock()
		return chs
	}

	cli.mutex.RUnlock()
	return chs
}

func (cli *ZrpcClient) insertNewChannel(methodName string, ch methodChannel) {
	chs := cli.getChannels(methodName)
	chs.Store(ch.MsgID(), ch)
}

func (cli *ZrpcClient) removeChannel(methodName string, msgid string) {
	chs := cli.getChannels(methodName)
	chs.Delete(msgid)
}

// Send 发送 pack
func (cli *ZrpcClient) Send(p *zrpc.Pack) (string, error) {
	return cli.zconn.Send(p)
}

// SpecifySend 向指定服务节点发送 pack
func (cli *ZrpcClient) SpecifySend(id string, p *zrpc.Pack) error {
	return cli.zconn.SpecifySend(id, p)
}

func (cli *ZrpcClient) Close() error {
	if cli.isClosed() {
		return nil
	}
	if cli.serviceDiscovery != nil {
		cli.serviceDiscovery.Stop()
	}

	cli.zconn.Close()
	close(cli._closed)
	return nil
}
