package v2router

import (
	"context"
	"fmt"
	"time"

	"github.com/coredns/coredns/plugin"
	clog "github.com/coredns/coredns/plugin/pkg/log"
	lru "github.com/hashicorp/golang-lru"
	"github.com/iosmanthus/geomatch"
	"github.com/miekg/dns"
)

const self = "v2router"

var log = clog.NewWithPlugin(self)

type V2Router struct {
	forwarders       []Forwarder
	defaultForwarder Forwarder
	matchers         []geomatch.Matcher
	cache            *lru.ARCCache
	Next             plugin.Handler
}

func logRtt(begin time.Time, key string) {
	used := time.Since(begin)
	format := "%s resolved, used %v"
	var logger func(format string, v ...interface{})
	if used >= time.Second {
		logger = log.Warningf
	} else {
		logger = log.Infof
	}
	logger(format, key, used)
}

func (v *V2Router) ServeDNS(ctx context.Context, w dns.ResponseWriter, m *dns.Msg) (int, error) {
	begin := time.Now()

	questions := m.Question
	matchers := v.matchers

	if questions == nil {
		return dns.RcodeFormatError, plugin.Error(self, fmt.Errorf("empty question"))
	}

	key := questions[0].Name

	defer logRtt(begin, key)

	if i, ok := v.cache.Get(key); ok {
		idx := i.(int)
		var f Forwarder

		if idx < 0 {
			f = v.defaultForwarder
		} else {
			f = v.forwarders[idx]
		}

		log.Infof("%s hits match cache, forward to %v", key, f)
		return f.Forward(ctx, w, m)
	}

	for j := range matchers {
		cond := matchers[j].Match(key)
		if cond == nil {
			continue
		}
		v.cache.Add(key, j)

		f := v.forwarders[j]

		log.Infof("%s matches rule: %v, forward to %v", key, cond, f)
		return f.Forward(ctx, w, m)
	}

	v.cache.Add(key, -1)

	// Use default
	log.Infof("%s matches default rule, forward to %v", key, v.defaultForwarder)
	return v.defaultForwarder.Forward(ctx, w, m)
}

func (v *V2Router) Ready() bool {
	return true
}

func (v *V2Router) Name() string {
	return self
}
