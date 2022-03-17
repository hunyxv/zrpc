package zrpc

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/go-zookeeper/zk"
)

type zookeeperRegister struct {
	metadata []byte
	cnf      *RegisterConfig
	client   *zk.Conn
}

func NewZookeeperRegister(cnf *RegisterConfig) (ServiceRegister, error) {
	zkClient, _, err := zk.Connect(cnf.Registries, time.Second*5)
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

	return &zookeeperRegister{
		cnf:      cnf,
		metadata: nodeInfo,
		client:   zkClient,
	}, nil
}

// Register 注册节点
func (zr *zookeeperRegister) Register() {
	if err := zr.createPNode(); err != nil {
		zr.cnf.Logger.Warnf("zookeeper register: path %s create fail, err: %v", zr.node(), err)
		return
	}

	exist, _, err := zr.client.Exists(zr.key())
	if err != nil {
		zr.cnf.Logger.Warnf("zookeeper register: unknow err, err: %v", err)
		return
	}

	if !exist {
		// 创建临时节点
		_, err = zr.client.Create(zr.key(), zr.metadata, zk.FlagEphemeral, zk.WorldACL(zk.PermAll))
		if err != nil {
			zr.cnf.Logger.Warnf("zookeeper register: path %s create fail, err: %v", zr.key(), err)
			return
		}
		return
	}

	_, stat, err := zr.client.Get(zr.key())
	if err != nil {
		zr.cnf.Logger.Warnf("zookeeper register: unknow err, err: %v", err)
		return
	}
	_, err = zr.client.Set(zr.key(), zr.metadata, stat.Aversion+1)
	if err != nil {
		zr.cnf.Logger.Warnf("zookeeper register: register fail, key: %s, data: %s err: %v", zr.key(), string(zr.metadata), err)
	}
}

func (zr *zookeeperRegister) createPNode() error {
	split := strings.Split(zr.node(), "/")
	pathPrefix := ""
	for _, seq := range split {
		if len(seq) == 0 {
			continue
		}

		pathPrefix = pathPrefix + "/" + seq
		exist, _, err := zr.client.Exists(pathPrefix)
		if err != nil {
			return err
		}

		if !exist {
			// 持久节点
			_, err = zr.client.Create(pathPrefix, nil, 0, zk.WorldACL(zk.PermAll))
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (zr *zookeeperRegister) node() string {
	return strings.Join([]string{
		zr.cnf.ServicePrefix,
		zr.cnf.ServerInfo.ServiceName}, "/")
}

func (zr *zookeeperRegister) key() string {
	return strings.Join([]string{
		zr.cnf.ServicePrefix,
		zr.cnf.ServerInfo.ServiceName,
		zr.cnf.ServerInfo.NodeID}, "/")
}

// Deregister 注销节点
func (er *zookeeperRegister) Deregister() {
	if er.client != nil {
		er.client.Close()
	}
}

type zookeeperDiscover struct {
	prefix string
	cnf    *DiscoverConfig

	client  *zk.Conn
	version map[string]int32
}

func NewZookeeperDiscover(cnf *DiscoverConfig) (ServiceDiscover, error) {
	zkClient, _, err := zk.Connect(cnf.Registries, time.Second*5)
	if err != nil {
		return nil, err
	}

	if cnf.Logger == nil {
		cnf.Logger = &logger{}
	}

	return &zookeeperDiscover{
		prefix:  strings.Join([]string{cnf.ServicePrefix, cnf.ServiceName}, "/"),
		cnf:     cnf,
		client:  zkClient,
		version: map[string]int32{},
	}, nil
}

// Watch 监控节点变化
func (zd *zookeeperDiscover) Watch(callback WatchCallback) {
	zd.getAllService(callback)

	for {
		children, _, eventch, err := zd.client.ChildrenW(zd.node())
		if err != nil {
			if err == zk.ErrConnectionClosed {
				return
			}
			zd.cnf.Logger.Warnf("zookeeper discover: watch path:%s fail, err: %v", zd.node(), err)
			time.Sleep(time.Second * 3)
			continue
		}

		for event := range eventch {
			for _, nodeid := range children {
				path := event.Path + "/" + nodeid
				exist, _, err := zd.client.Exists(path)
				if err != nil {
					continue
				}
				if !exist {
					callback.Delete(nodeid)
				} else {
					data, version, err := zd.getData(path)
					if err != nil {
						continue
					}
					if v, ok := zd.version[path]; ok && v != version {
						callback.Delete(nodeid)
					}
					callback.AddOrUpdate(nodeid, data)
				}
			}
		}
	}
}

func (zd *zookeeperDiscover) getAllService(callback WatchCallback) {
	children, _, err := zd.client.Children(zd.node())
	if err != nil {
		zd.cnf.Logger.Warnf("zookeeper discover: get path:%s children fail, err: %v", zd.node(), err)
		return
	}

	for _, child := range children {
		path := zd.key(child)
		data, version, err := zd.getData(path)
		if err != nil {
			zd.cnf.Logger.Warnf("zookeeper discover: get path:%s fail, err: %v", zd.node(), err)
			return
		}

		zd.version[path] = version
		callback.AddOrUpdate(child, data)
	}
}

func (zd *zookeeperDiscover) getData(path string) ([]byte, int32, error) {
	data, s, err := zd.client.Get(path)
	return data, s.Version, err
}

func (zd *zookeeperDiscover) node() string {
	return strings.Join([]string{
		zd.cnf.ServicePrefix,
		zd.cnf.ServiceName}, "/")
}

func (zr *zookeeperDiscover) key(nodeid string) string {
	return strings.Join([]string{
		zr.cnf.ServicePrefix,
		zr.cnf.ServiceName,
		nodeid}, "/")
}

// Stop 停止监控
func (ed *zookeeperDiscover) Stop() {
	if ed.client != nil {
		ed.client.Close()
	}
}
