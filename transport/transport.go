package transport

import (
	"sort"
	"time"

	"github.com/miekg/dns"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
)

var log = logrus.WithFields(logrus.Fields{
	"plugin": "transport",
})

type Options struct {
	Expire       time.Duration
	GC           time.Duration
	YieldTimeout time.Duration
	Address      string
	Network      string
}

// Transport is a connection cache for connection reusing.
type Transport struct {
	options *Options

	conns []*PersistentConn

	dial chan struct{}
	ret  chan *PersistentConn

	yield chan *PersistentConn

	done chan struct{}
}

type PersistentConn struct {
	*dns.Conn
	used time.Time
}

func NewTransport(options Options) *Transport {
	t := &Transport{
		options: &options,
		dial:    make(chan struct{}),
		ret:     make(chan *PersistentConn),
		yield:   make(chan *PersistentConn),
		done:    make(chan struct{}),
	}

	go t.start()

	return t
}

func (t *Transport) start() {
	ticker := time.NewTicker(t.options.GC)

Wait:
	for {
		select {
		case <-t.dial:
			if conns := t.conns; len(conns) > 0 {
				pc := conns[len(conns)-1]
				if time.Since(pc.used) < t.options.Expire {
					t.conns = t.conns[:len(conns)-1]
					t.ret <- pc
					continue Wait
				}
				t.conns = nil
				go closeConns(conns)
			}
			t.ret <- nil
		case pc := <-t.yield:
			t.conns = append(t.conns, pc)
		case <-ticker.C:
			t.cleanup(false)
		case <-t.done:
			t.cleanup(true)
			close(t.ret)
			return
		}
	}
}

func (t *Transport) cleanup(all bool) {
	start := time.Now()
	defer func() {
		log.WithFields(logrus.Fields{
			"duration": time.Since(start),
		}).Debugf("cleaned connection pool for %s", t.options.Address)
	}()

	staleTime := time.Now().Add(-t.options.Expire)
	conns := t.conns
	if len(conns) == 0 {
		return
	}
	if all {
		t.conns = nil
		go closeConns(conns)
		return
	}

	good := sort.Search(len(conns), func(i int) bool {
		return conns[i].used.After(staleTime)
	})
	t.conns = conns[good:]
	go closeConns(conns[:good])
}

func (t *Transport) Dial(timeout time.Duration) (*PersistentConn, bool, error) {
	dialTimer := prometheus.NewTimer(
		dialHistogramVec.With(map[string]string{
			"address": t.options.Address,
		}))
	defer dialTimer.ObserveDuration()
	t.dial <- struct{}{}
	pc := <-t.ret
	if pc != nil {
		return pc, true, nil
	}

	conn, err := dns.DialTimeout(t.options.Network, t.options.Address, timeout)
	if err != nil {
		return nil, false, err
	}

	return &PersistentConn{Conn: conn}, false, nil
}

func (t *Transport) Yield(pc *PersistentConn) {
	pc.used = time.Now()
	select {
	case t.yield <- pc:
		return
	case <-time.After(t.options.YieldTimeout):
		return
	}
}

func (t *Transport) Stop() {
	close(t.done)
}

func closeConns(conns []*PersistentConn) {
	for _, conn := range conns {
		conn.Close()
	}
}
