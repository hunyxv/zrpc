package zrpc

import (
	"context"
	"encoding/json"
	"net"
	"net/url"
	"strconv"
	"strings"

	consulapi "github.com/hashicorp/consul/api"
)

type consulRegister struct {
	ctx    context.Context
	cancel context.CancelFunc

	metadata map[string]string
	key      string
	cnf      *RegisterConfig
	client   *consulapi.Client
}

// NewConsulRegister consul 服务注册
func NewConsulRegister(cnf *RegisterConfig) (ServiceRegister, error) {
	if cnf.Logger == nil {
		cnf.Logger = &logger{}
	}

	consulConfig := consulapi.DefaultConfig()
	consulConfig.Address = strings.Join(cnf.Registries, ",")
	consulClient, err := consulapi.NewClient(consulConfig)
	if err != nil {
		return nil, err
	}

	metadata := map[string]string{
		"service_name":     cnf.ServerInfo.ServiceName,
		"nodeid":           cnf.ServerInfo.NodeID,
		"local_endpoint":   cnf.ServerInfo.LocalEndpoint,
		"cluster_endpoint": cnf.ServerInfo.ClusterEndpoint,
		"state_endpoint":   cnf.ServerInfo.StateEndpoint,
	}

	key := strings.Join([]string{
		cnf.ServicePrefix,
		cnf.ServerInfo.ServiceName}, "/")
	ctx, cancel := context.WithCancel(context.Background())
	return &consulRegister{
		ctx:    ctx,
		cancel: cancel,

		metadata: metadata,
		key:      key,
		cnf:      cnf,
		client:   consulClient,
	}, nil
}

// Register 注册节点
func (cr *consulRegister) Register() {
	u, err := url.Parse(cr.cnf.ServerInfo.LocalEndpoint)
	if err != nil {
		cr.cnf.Logger.Warnf("consul register: localendpoing parse fail, err: %v", err)
		return
	}
	host, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		cr.cnf.Logger.Warnf("consul register: , invalid host:port: %s err: %v", u.Host, err)
		return
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		cr.cnf.Logger.Warnf("consul register: , invalid port: %s err: %v", u.Port(), err)
		return
	}

	registration := &consulapi.AgentServiceRegistration{
		Kind:    consulapi.ServiceKindTypical,
		Address: host,
		Port:    port,
		Meta:    cr.metadata,
		ID:      cr.cnf.ServerInfo.NodeID,
		Name:    cr.cnf.ServerInfo.ServiceName,
		Tags:    []string{"zrpc"},
	}
	checkEndpoints := make([]*consulapi.AgentServiceCheck, 0, 3)
	checkEndpoints = append(checkEndpoints, &consulapi.AgentServiceCheck{
		Name:                           "local-endpoint",
		TCP:                            u.Host,
		Interval:                       "7s",
		Timeout:                        "3s",
		DeregisterCriticalServiceAfter: "30s",
	})

	u, err = url.Parse(cr.cnf.ServerInfo.ClusterEndpoint)
	if err != nil {
		cr.cnf.Logger.Warnf("consul register: clusterendpoing parse fail, err: %v", err)
		return
	}
	checkEndpoints = append(checkEndpoints, &consulapi.AgentServiceCheck{
		Name:                           "cluster-endpoint",
		TCP:                            u.Host,
		Interval:                       "7s",
		Timeout:                        "3s",
		DeregisterCriticalServiceAfter: "30s",
	})

	u, err = url.Parse(cr.cnf.ServerInfo.StateEndpoint)
	if err != nil {
		cr.cnf.Logger.Warnf("consul register: stateendpoing parse fail, err: %v", err)
		return
	}
	checkEndpoints = append(checkEndpoints, &consulapi.AgentServiceCheck{
		Name:                           "state-endpoint",
		TCP:                            u.Host,
		Interval:                       "7s",
		Timeout:                        "3s",
		DeregisterCriticalServiceAfter: "30s",
	})

	registration.Checks = checkEndpoints
	err = cr.client.Agent().ServiceRegister(registration)
	if err != nil {
		cr.cnf.Logger.Warnf("consul register: registry fail, err: %v", err)
	}
	return
}

// Deregister 注销节点
func (cr *consulRegister) Deregister() {
	cr.cancel()
	if cr.cnf.ServerInfo.NodeID != "" {
		cr.client.Agent().ServiceDeregister(cr.cnf.ServerInfo.NodeID)
	}
}

type consulDiscover struct {
	ctx    context.Context
	cancel context.CancelFunc

	key    string
	cnf    *DiscoverConfig
	client *consulapi.Client
}

// NewConsulDiscover consul 服务发现
func NewConsulDiscover(cnf *DiscoverConfig) (ServiceDiscover, error) {
	if cnf.Logger == nil {
		cnf.Logger = &logger{}
	}

	consulConfig := consulapi.DefaultConfig()
	consulConfig.Address = strings.Join(cnf.Registries, ",")
	consulClient, err := consulapi.NewClient(consulConfig)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &consulDiscover{
		ctx:    ctx,
		cancel: cancel,

		//key:    strings.Join([]string{cnf.ServicePrefix, cnf.ServiceName}, "/"),
		cnf:    cnf,
		client: consulClient,
	}, nil
}

// Watch 监控节点变化
func (cd *consulDiscover) Watch(callback WatchCallback) {
	var lastIndex uint64
	for {
		select {
		case <-cd.ctx.Done():
			return
		default:
			services, querymeta, err := cd.client.Health().Service(cd.cnf.ServiceName, "zrpc", true, &consulapi.QueryOptions{
				WaitIndex: lastIndex, // 同步点，这个调用将一直阻塞，直到有新的更新
			})
			if err != nil {
				cd.cnf.Logger.Warnf("consul discover: watch fail, err: %v", err)
				continue
			}
			lastIndex = querymeta.LastIndex

			for _, service := range services {
				meta := service.Service.Meta
				nodeid, ok := meta["nodeid"]
				if !ok {
					continue
				}
				switch service.Checks.AggregatedStatus() {
				case consulapi.HealthPassing:
					node, _ := json.Marshal(meta)
					callback.AddOrUpdate(nodeid, node)
				case consulapi.HealthWarning, consulapi.HealthCritical:
					callback.Delete(nodeid)
				}
			}

		}
	}
}

// Stop 停止监控
func (cd *consulDiscover) Stop() {
	cd.cancel()
}
