package discovery

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
	"google.golang.org/grpc/resolver"
)

const Scheme = "etcd"

func NewEtcdClient(endpoints []string, timeout time.Duration) (*clientv3.Client, error) {
	if len(endpoints) == 0 {
		endpoints = []string{"127.0.0.1:2379"}
	}
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	return clientv3.New(clientv3.Config{
		Endpoints:   endpoints,
		DialTimeout: timeout,
	})
}

type Registrar struct {
	client  *clientv3.Client
	service string
	addr    string
	ttl     int64
	key     string
}

func NewRegistrar(client *clientv3.Client, service string, addr string, ttl int64) *Registrar {
	if ttl <= 0 {
		ttl = 10
	}
	return &Registrar{
		client:  client,
		service: service,
		addr:    addr,
		ttl:     ttl,
		key:     serviceKey(service, addr),
	}
}

func (r *Registrar) Start(ctx context.Context) error {
	lease, err := r.client.Grant(ctx, r.ttl)
	if err != nil {
		return fmt.Errorf("grant etcd lease: %w", err)
	}
	if _, err := r.client.Put(ctx, r.key, r.addr, clientv3.WithLease(lease.ID)); err != nil {
		return fmt.Errorf("register service in etcd: %w", err)
	}

	keepAliveCh, err := r.client.KeepAlive(ctx, lease.ID)
	if err != nil {
		return fmt.Errorf("keepalive etcd lease: %w", err)
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				_, _ = r.client.Delete(context.Background(), r.key)
				return
			case _, ok := <-keepAliveCh:
				if !ok {
					return
				}
			}
		}
	}()
	return nil
}

type ResolverBuilder struct {
	client *clientv3.Client
}

func RegisterResolver(client *clientv3.Client) {
	resolver.Register(&ResolverBuilder{client: client})
}

func (b *ResolverBuilder) Scheme() string {
	return Scheme
}

func (b *ResolverBuilder) Build(target resolver.Target, cc resolver.ClientConn, opts resolver.BuildOptions) (resolver.Resolver, error) {
	r := &etcdResolver{
		client:  b.client,
		cc:      cc,
		service: target.Endpoint(),
		closeCh: make(chan struct{}),
	}
	if err := r.resolve(); err != nil {
		cc.ReportError(err)
	}
	go r.watch()
	return r, nil
}

type etcdResolver struct {
	client  *clientv3.Client
	cc      resolver.ClientConn
	service string
	closeCh chan struct{}
}

func (r *etcdResolver) ResolveNow(resolver.ResolveNowOptions) {
	_ = r.resolve()
}

func (r *etcdResolver) Close() {
	close(r.closeCh)
}

func (r *etcdResolver) resolve() error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	resp, err := r.client.Get(ctx, servicePrefix(r.service), clientv3.WithPrefix())
	if err != nil {
		return fmt.Errorf("resolve service %s: %w", r.service, err)
	}
	addresses := make([]resolver.Address, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		addr := strings.TrimSpace(string(kv.Value))
		if addr != "" {
			addresses = append(addresses, resolver.Address{Addr: addr})
		}
	}
	if len(addresses) == 0 {
		return fmt.Errorf("no endpoints for service %s", r.service)
	}
	return r.cc.UpdateState(resolver.State{
		Addresses:     addresses,
		ServiceConfig: r.cc.ParseServiceConfig(`{"loadBalancingConfig":[{"round_robin":{}}]}`),
	})
}

func (r *etcdResolver) watch() {
	watchCh := r.client.Watch(context.Background(), servicePrefix(r.service), clientv3.WithPrefix())
	for {
		select {
		case <-r.closeCh:
			return
		case _, ok := <-watchCh:
			if !ok {
				return
			}
			if err := r.resolve(); err != nil {
				r.cc.ReportError(err)
			}
		}
	}
}

func servicePrefix(service string) string {
	return path.Join("/short_url/services", service) + "/"
}

func serviceKey(service string, addr string) string {
	safeAddr := strings.NewReplacer("/", "_", ":", "_").Replace(addr)
	return path.Join(servicePrefix(service), safeAddr)
}
