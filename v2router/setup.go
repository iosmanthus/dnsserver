package v2router

import (
	"github.com/coredns/caddy"
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"
)

func init() {
	plugin.Register("v2router", setup)
}

func setup(c *caddy.Controller) error {
	p := NewParser()
	v, err := p.Parse(c)
	if err != nil {
		return plugin.Error(self, err)
	}

	dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
		v.Next = next
		return v
	})

	c.OnShutdown(func() error {
		forwarders := append(v.forwarders, v.defaultForwarder)
		for _, f := range forwarders {
			switch forwarder := f.(type) {
			case *UpstreamsForwarder:
				for _, pool := range forwarder.transports {
					pool.Stop()
				}
			}
		}
		return nil
	})

	return nil
}
