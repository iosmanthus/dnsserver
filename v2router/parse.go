package v2router

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/plugin"
	lru "github.com/hashicorp/golang-lru"
	"github.com/iosmanthus/geomatch"
)

const (
	defaultRetry      = 8
	defaultTimeout    = time.Millisecond * 500
	defaultMatchCache = 4096
)

type Parser struct {
	geosite          string
	forwarders       []Forwarder
	matchers         []geomatch.Matcher
	defaultForwarder Forwarder
}

func NewParser() *Parser {
	return &Parser{}
}

func (p *Parser) Parse(c *caddy.Controller) (*V2Router, error) {
	var (
		err error
		i   = 0
	)

	for c.Next() {
		if i > 0 {
			return nil, plugin.ErrOnce
		}
		i++

		err = p.parseArgs(c)
		if err != nil {
			return nil, c.Errf("%v", err)
		}

		err = p.parseBlock(c)
		if err != nil {
			return nil, c.Errf("%v", err)
		}
	}

	cache, err := lru.NewARC(defaultMatchCache)
	if err != nil {
		return nil, err
	}
	return &V2Router{
		forwarders:       p.forwarders,
		defaultForwarder: p.defaultForwarder,
		matchers:         p.matchers,
		cache:            cache,
		Next:             nil,
	}, nil
}

func (p *Parser) parseArgs(c *caddy.Controller) error {
	if !c.Next() {
		return c.ArgErr()
	}

	p.geosite = c.Val()
	return nil
}

func (p *Parser) parseBlock(c *caddy.Controller) error {
	var (
		err              error
		hasDefault       bool
		forwarders       []Forwarder
		matchers         []geomatch.Matcher
		defaultForwarder Forwarder
	)

	for c.NextBlock() {
		switch c.Val() {
		case "forward":
			forward, matcher, err := p.parseForward(c)
			if err != nil {
				return err
			}
			forwarders = append(forwarders, forward)
			matchers = append(matchers, matcher)
		case "reject":
			reject, matcher, err := p.parseReject(c)
			if err != nil {
				return c.Errf("%v", err)
			}
			forwarders = append(forwarders, reject)
			matchers = append(matchers, matcher)
		case "default":
			if hasDefault {
				return errors.New("multiple default routes detected")
			}
			hasDefault = true
			defaultForwarder, err = p.parseDefault(c)
			if err != nil {
				return err
			}
		default:
			return fmt.Errorf("unknown property '%s'", c.Val())
		}
	}

	if !hasDefault {
		return errors.New("expected a default route")
	}

	p.forwarders = forwarders
	p.matchers = matchers
	p.defaultForwarder = defaultForwarder
	return nil
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
	retry   int
	timeout time.Duration
}

func parseAttribute(args []string) (*attributes, error) {
	attr := &attributes{
		retry:   defaultRetry,
		timeout: defaultTimeout,
	}
	for _, arg := range args {
		kv := strings.SplitN(arg, ":", 2)
		if len(kv) != 2 {
			return nil, errors.New("invalid attribute")
		}
		k, v := kv[0], kv[1]
		switch k {
		case "timeout":
			timeout, err := time.ParseDuration(v)
			if err != nil {
				return nil, err
			}
			attr.timeout = timeout
		case "retry":
			retry, err := strconv.Atoi(v)
			if err != nil {
				return nil, err
			}
			attr.retry = retry
		default:
			return nil, errors.New("invalid attribute")
		}
	}
	return attr, nil
}

func (p *Parser) parseForward(c *caddy.Controller) (Forwarder, geomatch.Matcher, error) {
	args := c.RemainingArgs()
	l := len(args)
	if l < 3 {
		return nil, nil, errors.New("missing arguments in forward")
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

	forward, err := NewPlainForwarder(addrs, attr)
	if err != nil {
		return nil, nil, err
	}

	matcher, err := geomatch.
		NewDomainMatcherBuilder().
		From(p.geosite).
		AddConditions(args[:idx]...).
		Build()

	if err != nil {
		return nil, nil, err
	}

	return forward, matcher, nil
}

func (p *Parser) parseDefault(c *caddy.Controller) (Forwarder, error) {
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
	return NewPlainForwarder(addrs, attr)
}

func (p *Parser) parseReject(c *caddy.Controller) (Forwarder, geomatch.Matcher, error) {
	args := c.RemainingArgs()

	if len(args) == 0 {
		return nil, nil, errors.New("missing matchers")
	}

	matcher, err := geomatch.NewDomainMatcherBuilder().
		From(p.geosite).
		AddConditions(args...).
		Build()

	if err != nil {
		return nil, nil, err
	}

	return NewReject(), matcher, nil
}
