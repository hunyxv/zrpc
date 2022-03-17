package client

import (
	"sync"

	"github.com/hunyxv/zrpc"
)

var (
	once sync.Once

	client *zrpcClient
)

type zrpcClient struct {
	chanManager      *channelManager
	zconn            zConnecter
	serviceDiscovery zrpc.ServiceDiscover
	opts             *options
	isClosed         bool
}

func NewDirectClient(server ServerInfo, opts ...Option) (*zrpcClient, error) {
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
		client = &zrpcClient{
			opts:        defopts,
			zconn:       conn,
			chanManager: newChannelManager(conn),
		}
	})
	return client, err
}

func NewClient(discover zrpc.ServiceDiscover, opts ...Option) (*zrpcClient, error) {
	var err error
	once.Do(func() {
		defopts := &options{
			Discover: discover,
		}
		for _, f := range opts {
			f(defopts)
		}

		var conn *connectManager
		conn, err = newConnectManager(defopts.Identity)
		if err != nil {
			return
		}
		client = &zrpcClient{
			opts:             defopts,
			zconn:            conn,
			serviceDiscovery: discover,
			chanManager:      newChannelManager(conn),
		}
		go client.serviceDiscovery.Watch(conn)
	})
	return client, err
}

// Decorator 将 server proxy 装饰为可调用 server
func (cli *zrpcClient) Decorator(name string, i interface{}, retry int) error {
	proxy := newInstanceProxy(name, i, cli.chanManager)
	return proxy.init()
}

func (cli *zrpcClient) Run() {
	for !cli.isClosed {
		cli.chanManager.start()
	}
}

func (cli *zrpcClient) Close() error {
	if cli.serviceDiscovery != nil {
		cli.serviceDiscovery.Stop()
	}
	cli.isClosed = true

	cli.zconn.Close()
	return nil
}
