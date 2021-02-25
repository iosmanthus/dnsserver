package v2router

import (
	"errors"
	"fmt"
	lru "github.com/hashicorp/golang-lru"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"
	"github.com/iosmanthus/geomatch"
)

func init() {
	plugin.Register("v2router", setup)
}

func parse(c *caddy.Controller) (*V2Router, error) {
	var err error
	var router = new(V2Router)

	i := 0
	for c.Next() {
		if i > 0 {
			return nil, plugin.ErrOnce
		}
		i++

		err = router.parseArgs(c)
		if err != nil {
			return nil, err
		}

		err = router.parseBlock(c)
		if err != nil {
			return nil, err
		}
	}

	router.cache, err = lru.NewARC(4096)
	if err != nil {
		return nil, err
	}

	return router, nil
}

func (v *V2Router) parseBlock(c *caddy.Controller) error {

	var (
		err        error
		hasDefault bool
		routers    []*router
		matchers   []*geomatch.DomainMatcher
		last       *router
	)

	for c.NextBlock() {
		switch c.Val() {
		case "route":
			router, matcher, err := v.parseRoute(c)
			if err != nil {
				return c.Errf("%v", err)
			}
			routers = append(routers, router)
			matchers = append(matchers, matcher)
		case "forbid":
			matcher, err := v.parseForbid(c)
			if err != nil {
				return c.Errf("%v", err)
			}
			routers = append(routers, nil)
			matchers = append(matchers, matcher)
		case "default":
			if hasDefault {
				return c.Err("multiple default routes detected")
			}
			hasDefault = true
			last, err = parseDefault(c)
			if err != nil {
				return c.Errf("%v", err)
			}
		default:
			return c.Errf("unknown property '%s'", c.Val())
		}
	}

	if !hasDefault {
		return c.Err("expected a default route")
	}

	*v = V2Router{
		dataPath: v.dataPath,
		routers:  routers,
		matchers: matchers,
		last:     last,
		Next:     nil,
	}
	return nil
}

func (v *V2Router) parseArgs(c *caddy.Controller) error {
	if !c.Next() {
		return c.ArgErr()
	}
	v.dataPath = c.Val()
	return nil
}

func (v *V2Router) parseForbid(c *caddy.Controller) (*geomatch.DomainMatcher, error) {
	args := c.RemainingArgs()
	if len(args) == 0 {
		return nil, errors.New("missing domain matchers")
	}

	matcher, err := geomatch.NewDomainMatcherBuilder().
		From(v.dataPath).
		AddConditions(args...).
		Build()
	if err != nil {
		return nil, err
	}
	return matcher, nil
}

func parseDefault(c *caddy.Controller) (*router, error) {
	args := c.RemainingArgs()
	if len(args) == 0 {
		return nil, errors.New("missing default router")
	}
	addrs, err := parseUpstream(args[0])
	if err != nil {
		return nil, err
	}
	attr, err := parseAttribute(args[1:])
	if err != nil {
		return nil, err
	}
	return newRouter(addrs, attr), nil
}

func parseInt(c *caddy.Controller) (int, error) {
	if !c.Next() {
		return 0, fmt.Errorf("expected a integer")
	}
	return strconv.Atoi(c.Val())
}

func parseCache(c *caddy.Controller) (int, error) {
	size, err := parseInt(c)
	if err != nil {
		return 0, err
	}
	if size <= 0 {
		return 0, errors.New("cache size should be a positive value")
	}
	return size, nil
}

func parseRetry(v string) (int, error) {
	times, err := strconv.Atoi(v)
	if err != nil {
		return 0, err
	}
	if times <= 0 {
		return 0, errors.New("retry times should be a positive value")
	}
	return times, nil
}

func parseTimeout(v string) (time.Duration, error) {
	timeout, err := strconv.Atoi(v)
	if err != nil {
		return 0, err
	}
	if timeout <= 0 {
		return 0, errors.New("retry times should be a positive value")
	}
	return time.Second * time.Duration(timeout), nil
}

func parseConnections(v string) (int, error) {
	count, err := strconv.Atoi(v)
	if err != nil {
		return 0, err
	}
	if count < 0 {
		return 0, errors.New("connection count should be a positive value")
	}
	return count, nil
}

func parseUpstream(addr string) ([]net.Addr, error) {
	var (
		addresses []net.Addr
		err       error
	)

	for len(addr) > 0 {
		semicolon := len(addr)
		var address net.Addr
		for i := range addr {
			if addr[i] == ';' {
				semicolon = i
				break
			}
		}
		switch {
		case strings.HasPrefix(addr, "dns://"), strings.HasPrefix(addr, "udp://"):
			address, err = net.ResolveUDPAddr("udp", addr[6:semicolon])
		case strings.HasPrefix(addr, "tcp://"):
			address, err = net.ResolveTCPAddr("tcp", addr[6:semicolon])
		default:
			address, err = net.ResolveUDPAddr("udp", addr)
		}
		if err != nil {
			return nil, err
		}
		addresses = append(addresses, address)
		if semicolon < len(addr) {
			addr = addr[semicolon+1:]
		} else {
			addr = addr[semicolon:]
		}
	}
	return addresses, nil
}

type attributes struct {
	connections int
	retry       int
	timeout     time.Duration
}

func parseAttribute(args []string) (*attributes, error) {
	var attr attributes
	for _, arg := range args {
		kv := strings.SplitN(arg, ":", 2)
		if len(kv) != 2 {
			return nil, errors.New("invalid attribute")
		}
		k, v := kv[0], kv[1]
		switch k {
		case "connections":
			cnt, err := parseConnections(v)
			if err != nil {
				return nil, err
			}
			attr.connections = cnt
		case "timeout":
			timeout, err := parseTimeout(v)
			if err != nil {
				return nil, err
			}
			attr.timeout = timeout
		case "retry":
			cnt, err := parseRetry(v)
			if err != nil {
				return nil, err
			}
			attr.retry = cnt
		default:
			return nil, errors.New("invalid attribute")
		}
	}
	if attr.retry == 0 {
		attr.retry = 2
	}
	if attr.connections == 0 {
		attr.connections = 4
	}
	if attr.timeout == 0 {
		attr.timeout = time.Second * 2
	}
	return &attr, nil
}

func (v *V2Router) parseRoute(c *caddy.Controller) (*router, *geomatch.DomainMatcher, error) {
	args := c.RemainingArgs()
	l := len(args)
	if l < 3 {
		return nil, nil, fmt.Errorf("route property expects at least 3 arguments, got %d", l)
	}

	idx := 0
	for i, arg := range args {
		if arg == "to" {
			idx = i
			break
		}
	}

	if idx == 0 || idx == l-1 {
		return nil, nil, errors.New("expected syntax: route `FROM`... to `dst...` [attributes]")
	}

	addrs, err := parseUpstream(args[idx+1])
	if err != nil {
		return nil, nil, err
	}

	attr, err := parseAttribute(args[idx+2:])
	if err != nil {
		return nil, nil, err
	}

	router := newRouter(addrs, attr)

	matcher, err := geomatch.
		NewDomainMatcherBuilder().
		From(v.dataPath).
		AddConditions(args[:idx]...).
		Build()

	if err != nil {
		return nil, nil, err
	}

	return router, matcher, nil
}

func setup(c *caddy.Controller) error {
	v, err := parse(c)
	if err != nil {
		return plugin.Error(self, err)
	}

	dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
		v.Next = next
		return v
	})

	c.OnShutdown(func() error {
		routers := append(v.routers, v.last)
		for _, rt := range routers {
			if rt == nil {
				continue
			}
			for _, p := range rt.pools {
				if p.inner != nil {
					p.inner.Release()
				}
			}
		}
		return nil
	})

	return nil
}
