package zrpc

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

type etcdRegister struct {
	ctx    context.Context
	cancel context.CancelFunc

	metadata string
	key      string
	cnf      *RegisterConfig
	client   *clientv3.Client
	leaseID  clientv3.LeaseID // 租约 id
}

func NewEtcdRegistry(cnf *RegisterConfig) (ServiceRegister, error) {
	etcdClient, err := clientv3.New(clientv3.Config{Endpoints: cnf.Registries})
	if err != nil {
		return nil, err
	}

	nodeInfo, err := json.Marshal(cnf.ServerInfo)
	if err != nil {
		return nil, err
	}
	if cnf.Logger == nil {
		cnf.Logger = &logger{}
	}

	key := strings.Join([]string{
		cnf.ServicePrefix,
		cnf.ServerInfo.ServiceName,
		cnf.ServerInfo.NodeID}, "/")
	ctx, cancel := context.WithCancel(context.Background())
	return &etcdRegister{
		ctx:    ctx,
		cancel: cancel,

		metadata: string(nodeInfo),
		key:      key,
		cnf:      cnf,
		client:   etcdClient,
	}, nil
}

// Register 注册节点
func (er *etcdRegister) Register() {
	tick := time.NewTicker(er.cnf.HeartBeatPeriod)
	defer tick.Stop()

	for {
		select {
		case <-er.ctx.Done():
			return
		case <-tick.C:
			if er.leaseID > 0 {
				if err := er.leaseRenewal(); err != nil {
					er.cnf.Logger.Warnf("etcd register: endpoind: %s, leaseid: %d, err: %v", er.cnf.ServerInfo.LocalEndpoint, er.leaseID, err)
					er.leaseID = 0
					continue
				}

				er.cnf.Logger.Debugf("etcd register: endpoind: %s renewal succ", er.cnf.ServerInfo.LocalEndpoint)
			} else {
				if err := er.register(); err != nil {
					er.cnf.Logger.Warnf("etcd register: endpoind: %s register fail, err: %v", er.cnf.ServerInfo.LocalEndpoint, err)
					continue
				}
				er.cnf.Logger.Infof("etcd register: endpoind: %s register succ", er.cnf.ServerInfo.LocalEndpoint)
			}
		}
	}
}

// 注册
func (er *etcdRegister) register() error {
	ctx, cancel := context.WithTimeout(er.ctx, time.Second*5)
	defer cancel()
	resp, err := er.client.Grant(ctx, int64(er.cnf.HeartBeatPeriod.Seconds())+3)
	if err != nil {
		return err
	}

	_, err = er.client.Put(ctx, er.key, er.metadata, clientv3.WithLease(resp.ID))
	er.leaseID = resp.ID
	return err
}

// 续租
func (er *etcdRegister) leaseRenewal() error {
	ctx, cancel := context.WithTimeout(er.ctx, time.Second*5)
	defer cancel()
	_, err := er.client.KeepAliveOnce(ctx, er.leaseID)
	return err
}

// Deregister 注销节点
func (er *etcdRegister) Deregister() {
	er.cancel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := er.client.Delete(ctx, er.key)
	if err != nil {
		er.cnf.Logger.Error("etcd register: endpoind: %s deregister fail, err: %v", er.key, err)
	}
}

type etcdDiscover struct {
	ctx    context.Context
	cancel context.CancelFunc

	prefix string
	cnf    *DiscoverConfig
	client *clientv3.Client
}

func NewEtcdDiscover(cnf *DiscoverConfig) (ServiceDiscover, error) {
	etcdClient, err := clientv3.New(clientv3.Config{Endpoints: cnf.Registries})
	if err != nil {
		return nil, err
	}

	if cnf.Logger == nil {
		cnf.Logger = &logger{}
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &etcdDiscover{
		ctx:    ctx,
		cancel: cancel,

		prefix: strings.Join([]string{cnf.ServicePrefix, cnf.ServiceName}, "/"),
		cnf:    cnf,
		client: etcdClient,
	}, nil
}

// Watch 监控节点变化
func (ed *etcdDiscover) Watch(callback WatchCallback) {
	ed.getAllService(callback)

	watch := ed.client.Watch(ed.ctx, ed.prefix, clientv3.WithPrefix())
	for {
		select {
		case <-ed.ctx.Done():
			return
		case ret := <-watch:
			if err := ret.Err(); err != nil {
				ed.cnf.Logger.Errorf("etcd discover: watch err, err: %v", err)
				continue
			}
			for _, event := range ret.Events {
				if event.Kv == nil {
					continue
				}

				_, nodeid := splitKey(string(event.Kv.Key))
				switch event.Type {
				case clientv3.EventTypePut:
					callback.AddOrUpdate(nodeid, event.Kv.Value)
				case clientv3.EventTypeDelete:
					callback.Delete(nodeid)
				}
			}
		}
	}
}

func (ed *etcdDiscover) getAllService(callback WatchCallback) {
	ctx, cancel := context.WithTimeout(ed.ctx, time.Second*5)
	defer cancel()
	result, err := ed.client.Get(ctx, ed.prefix, clientv3.WithPrefix())
	if err != nil {
		ed.cnf.Logger.Warnf("etcd discover: etcd-client Get() fail, err: %v", err)
		return
	}

	for _, kv := range result.Kvs {
		_, nodeid := splitKey(string(kv.Key))
		if err := callback.AddOrUpdate(nodeid, kv.Value); err != nil {
			ed.cnf.Logger.Warnf("etcd discover: endpoint: %s AddOrUpdate fail, err: %v", nodeid, err)
		}
	}
}

// Stop 停止监控
func (ed *etcdDiscover) Stop() {
	ed.cancel()
}

func splitKey(key string) (string, string) {
	l := strings.Split(key, "/")
	if len(l) > 2 {
		return l[len(l)-2], l[len(l)-1]
	}
	return "", ""
}
