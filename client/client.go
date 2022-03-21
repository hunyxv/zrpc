package client

import (
	"fmt"
	"sync"

	"github.com/hunyxv/zrpc"
	"github.com/pborman/uuid"
)

var (
	once sync.Once

	client *ZrpcClient
)

type ZrpcClient struct {
	chanManager      *channelManager
	zconn            zConnecter
	serviceDiscovery zrpc.ServiceDiscover
	services         sync.Map
	opts             *options
	isClosed         bool
}

// NewDirectClient 根据 rpc 服务地址连接的客户端（只适用于单节点服务）
func NewDirectClient(server ServerInfo, opts ...Option) (*ZrpcClient, error) {
	var err error
	once.Do(func() {
		defopts := &options{
			Identity:   "cli-" + zrpc.NewMessageID(),
			ServerAddr: &server,
		}
		for _, f := range opts {
			f(defopts)
		}

		var conn zConnecter
		conn, err = newDirectConnect(defopts.Identity, server)
		if err != nil {
			return
		}
		client = &ZrpcClient{
			opts:        defopts,
			zconn:       conn,
			chanManager: newChannelManager(conn),
		}
		go client.chanManager.start()
	})
	return client, err
}

// NewClient 使用注册/发现中心创建客户端
func NewClient(discover zrpc.ServiceDiscover, opts ...Option) (*ZrpcClient, error) {
	var err error
	once.Do(func() {
		defopts := &options{
			Discover: discover,
			Identity: uuid.NewRandom().String(),
		}
		for _, f := range opts {
			f(defopts)
		}

		var conn *connectManager
		conn, err = newConnectManager(defopts.Identity)
		if err != nil {
			return
		}
		client = &ZrpcClient{
			opts:             defopts,
			zconn:            conn,
			serviceDiscovery: discover,
			chanManager:      newChannelManager(conn),
		}
		go client.serviceDiscovery.Watch(conn)
		go client.chanManager.start()
	})
	return client, err
}

// Decorator 将 server proxy 装饰为可调用 server
func (cli *ZrpcClient) Decorator(name string, i interface{}, retry int) error {
	_, ok := cli.services.LoadOrStore(name, i)
	if ok {
		return fmt.Errorf("service [%s] is exists", name)
	}

	proxy := newInstanceProxy(name, i, cli.chanManager)
	err := proxy.init()
	if err != nil {
		cli.services.Delete(name)
		return err
	}

	return nil
}

// GetSerivce 获取已注册 rpc 服务实例以供使用
func (cli *ZrpcClient) GetSerivce(name string) (interface{}, bool) {
	return cli.services.Load(name)
}

func (cli *ZrpcClient) Close() error {
	if cli.serviceDiscovery != nil {
		cli.serviceDiscovery.Stop()
	}

	cli.isClosed = true
	cli.zconn.Close()
	cli.chanManager.Close()
	return nil
}
