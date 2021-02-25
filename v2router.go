package v2router

import (
	"context"
	"errors"
	"fmt"
	lru "github.com/hashicorp/golang-lru"
	"net"
	"sync"
	"time"

	"github.com/coredns/coredns/plugin"
	clog "github.com/coredns/coredns/plugin/pkg/log"
	"github.com/iosmanthus/geomatch"
	"github.com/miekg/dns"
	connpool "github.com/silenceper/pool"
)

const self = "v2router"

var log = clog.NewWithPlugin(self)

type V2Router struct {
	dataPath string
	routers  []*router
	last     *router
	matchers []*geomatch.DomainMatcher
	cache    *lru.ARCCache
	Next     plugin.Handler
}

type pool struct {
	sync.Mutex
	inner  connpool.Pool
	config *connpool.Config
}

type router struct {
	addrs   []net.Addr
	clients []*dns.Client
	pools   []*pool
	retry   int
}

func newConn(addr net.Addr) (*dns.Conn, error) {
	conn, err := dns.Dial(addr.Network(), addr.String())
	if err != nil {
		return nil, err
	}
	if tcpConn, ok := conn.Conn.(*net.TCPConn); ok {
		if err = tcpConn.SetKeepAlive(true); err != nil {
			return nil, err
		}
		if err = tcpConn.SetKeepAlivePeriod(time.Second * 1); err != nil {
			return nil, err
		}
	}

	return conn, err
}

func healthCheck(conn *dns.Conn) error {
	ping := new(dns.Msg)
	ping.SetQuestion(".", dns.TypeNS)

	client := new(dns.Client)
	m, _, err := client.ExchangeWithConn(ping, conn)
	if err != nil && m != nil {
		// Silly check, something sane came back.
		if m.Response || m.Opcode == dns.OpcodeQuery {
			err = nil
		}
	}

	return err
}

func newRouter(addrs []net.Addr, attr *attributes) *router {
	connections := attr.connections
	router := &router{
		retry: attr.retry,
		addrs: addrs,
	}

	for i := range addrs {
		addr := addrs[i]
		config := &connpool.Config{
			InitialCap: connections,
			MaxCap:     connections,
			MaxIdle:    connections,
			Factory:    func() (interface{}, error) { return newConn(addr) },
			Close:      func(c interface{}) error { return c.(*dns.Conn).Close() },
			Ping:       func(c interface{}) error { return healthCheck(c.(*dns.Conn)) },
		}

		client := new(dns.Client)
		client.Timeout = attr.timeout

		router.clients = append(router.clients, client)
		router.pools = append(router.pools, &pool{config: config})
	}

	return router
}

func (v *V2Router) ServeDNS(ctx context.Context, w dns.ResponseWriter, m *dns.Msg) (int, error) {
	begin := time.Now()
	questions := m.Question
	matchers := v.matchers

	if questions == nil {
		return dns.RcodeFormatError, plugin.Error(self, fmt.Errorf("empty question"))
	}

	key := questions[0].Name
	if i, ok := v.cache.Get(key); ok {
		idx := i.(int)
		var rt *router

		if idx == -1 {
			rt = v.last
		} else {
			rt = v.routers[idx]
		}

		if rt != nil {
			log.Infof("%s hits cache, resolve with endpoint %s", key, rt.addrs)
		}

		return rt.exchange(ctx, begin, w, m)
	}

	for j := range matchers {
		cond := matchers[j].Match(key)
		if cond == nil {
			continue
		}
		v.cache.Add(key, j)

		rt := v.routers[j]

		if rt != nil {
			log.Infof("%s matches rule: %v, forward to %s", key, cond, rt.addrs)
		}
		return rt.exchange(ctx, begin, w, m)
	}

	v.cache.Add(key, -1)

	// Use default
	log.Infof("%s matches default rule, forward to %s", key, v.last.addrs)

	return v.last.exchange(ctx, begin, w, m)
}

func (r *router) initConnPool() bool {
	for _, p := range r.pools {
		if p.inner != nil {
			continue
		}

		p.Lock()
		var err error
		if p.inner, err = connpool.NewChannelPool(p.config); err != nil {
			p.Unlock()
			log.Warning("fail to initialize connection pool: %v", err)
			return false
		}

		p.Unlock()
	}
	return true
}

func dummyResponse(req *dns.Msg) *dns.Msg {
	dummy := &dns.Msg{}
	dummy.SetReply(req)
	dummy.Answer = []dns.RR{
		&dns.A{A: net.ParseIP("127.0.0.1")},
	}
	return dummy
}

func (r *router) exchange(ctx context.Context, begin time.Time, w dns.ResponseWriter, req *dns.Msg) (int, error) {
	key := req.Question[0].Name
	if r == nil {
		log.Infof("blocked %s", key)
		w.WriteMsg(dummyResponse(req))
		return dns.RcodeSuccess, nil
	}

	if !r.initConnPool() {
		return dns.RcodeRefused, errors.New("server not available")
	}

	type dnsResponse struct {
		order int
		err   error
		resp  *dns.Msg
	}

	var (
		err    error
		result dnsResponse
	)

LOOP:
	for i := 0; i < r.retry+1; i++ {
		responses := make(chan dnsResponse, 1)

		for i := range r.addrs {
			go func(i int) {
				pool := r.pools[i].inner
				managedConn, err := pool.Get()
				if err != nil {
					log.Errorf("fail to get a connection from pool to %s", r.addrs[i])
					responses <- dnsResponse{i, err, nil}
					return
				}

				rawConn := managedConn.(*dns.Conn)
				resp, _, err := r.clients[i].ExchangeWithConn(req, rawConn)
				if err != nil {
					pool.Close(managedConn)
				} else {
					pool.Put(managedConn)
				}
				responses <- dnsResponse{i, err, resp}
			}(i)
		}

		select {
		case <-ctx.Done():
			return dns.RcodeServerFailure, ctx.Err()
		case result = <-responses:
			for i := 0; i < len(r.addrs); i++ {
				if err = result.err; err != nil {
					if i < len(r.addrs)-1 {
						result = <-responses
					}
					continue
				}
				if result.err != nil {
					continue
				}
				w.WriteMsg(result.resp)
				break LOOP
			}
		}
	}

	addr := r.addrs[result.order]
	used := time.Since(begin)
	if err != nil {
		log.Warningf("fail to resolve %s: %v, used %s via %s", key, err, used, addr)
		return dns.RcodeServerFailure, err
	}

	log.Infof("resolved %s, used %s via %s", key, used, addr)
	return dns.RcodeSuccess, nil
}

func (v *V2Router) Ready() bool {
	return true
}

func (v *V2Router) Name() string {
	return self
}
