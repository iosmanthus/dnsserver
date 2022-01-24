package v2router

import (
	"context"
	"fmt"
	"github.com/iosmanthus/dnsserver/request"
	"net"
	"time"

	"github.com/cenkalti/backoff"
	"github.com/iosmanthus/dnsserver/transport"
	"github.com/miekg/dns"
)

const (
	defaultYieldTimeout = time.Millisecond * 500
	defaultGCPeriod     = time.Second * 10
	defaultExpire       = time.Second * 10
)

type UpstreamsForwarder struct {
	transports []*transport.Transport
	clients    []*dns.Client
	addrs      []net.Addr
	timeout    time.Duration
	retry      int
}

func NewPlainForwarder(addrs []net.Addr, attr *attributes) (*UpstreamsForwarder, error) {
	var (
		clients    []*dns.Client
		transports []*transport.Transport
	)

	for i := 0; i < len(addrs); i++ {
		client := new(dns.Client)
		client.Timeout = attr.timeout
		clients = append(clients, client)
	}

	transports = make([]*transport.Transport, len(addrs))
	var (
		err     error
		success int
	)

	for i := range transports {
		addr := addrs[i]
		transports[i] = transport.NewTransport(transport.Options{
			Expire:       defaultExpire,
			GC:           defaultGCPeriod,
			YieldTimeout: defaultYieldTimeout,
			Network:      addr.Network(),
			Address:      addr.String(),
		})
		if err != nil {
			break
		}
		success = i
	}

	if err != nil {
		for i := 0; i <= success; i++ {
			transports[i].Stop()
		}
	}

	return &UpstreamsForwarder{
		transports: transports,
		clients:    clients,
		addrs:      addrs,
		retry:      attr.retry,
		timeout:    attr.timeout,
	}, nil
}

func (f *UpstreamsForwarder) String() string {
	return fmt.Sprintf("%v", f.addrs)
}

type result struct {
	err   error
	ok    *dns.Msg
	index int
}

func getQuestionName(m *dns.Msg) string {
	var name = "UNKNOWN"
	if m != nil && len(m.Question) > 0 {
		name = m.Question[0].Name
	}
	return name
}

func (f *UpstreamsForwarder) doExchange(ctx context.Context, index int, m *dns.Msg) (*dns.Msg, error) {
	log := request.WithLogger(ctx, log)

	pc, cached, err := f.transports[index].Dial(f.timeout)
	if err != nil {
		return nil, err
	}
	r, _, err := f.clients[index].ExchangeWithConn(m, pc.Conn)
	if err != nil {
		go pc.Close()
		return nil, err
	}

	key := getQuestionName(m)
	if cached {
		connectionCacheHitCounterVec.WithLabelValues(f.addrs[index].String()).Inc()
		log.Infof("using cached connection for %s to %v", key, f.addrs[index])
	} else {
		connectionCacheMissCounterVec.WithLabelValues(f.addrs[index].String()).Inc()
		log.Infof("using new connection for %s to %v", key, f.addrs[index])
	}
	f.transports[index].Yield(pc)
	return r, nil
}

func (f *UpstreamsForwarder) exchange(ctx context.Context, index int, m *dns.Msg, ch chan<- result) {
	backoffer := backoff.WithMaxRetries(backoff.NewExponentialBackOff(), uint64(f.retry))
	boCtx := backoff.WithContext(backoffer, ctx)
	err := backoff.Retry(func() error {
		resp, err := f.doExchange(ctx, index, m)
		if err != nil {
			return err
		}
		ch <- result{
			ok:    resp,
			index: index,
		}
		return nil
	}, boCtx)
	if err != nil {
		ch <- result{
			err:   err,
			index: index,
		}
	}
}

func (f *UpstreamsForwarder) Forward(ctx context.Context, w dns.ResponseWriter, m *dns.Msg) (int, error) {
	log := request.WithLogger(ctx, log)

	responses := make(chan result, len(f.addrs))
	cancels := make([]context.CancelFunc, len(f.addrs))
	for i := range f.addrs {
		ctx, cancel := context.WithCancel(ctx)
		cancels[i] = cancel
		go f.exchange(ctx, i, m, responses)
	}
	select {
	case <-ctx.Done():
		return dns.RcodeServerFailure, ctx.Err()
	case resp := <-responses:
		for i, cancel := range cancels {
			if i != resp.index {
				cancel()
			}
		}
		go f.ignoreResponses(ctx, responses, m)

		if err := resp.err; err != nil {
			return dns.RcodeServerFailure, err
		}

		log.Infof("accepts response from %s for %s", f.addrs[resp.index], getQuestionName(m))
		upstreamGaugeVec.With(map[string]string{
			"upstream": f.addrs[resp.index].String(),
		}).Inc()

		_ = w.WriteMsg(resp.ok)
		return resp.ok.Rcode, nil
	}
}

func (f *UpstreamsForwarder) ignoreResponses(ctx context.Context, responses <-chan result, m *dns.Msg) {
	log := request.WithLogger(ctx, log)

	for i := 0; i < len(f.addrs)-1; i++ {
		resp := <-responses
		key := getQuestionName(m)
		if err := resp.err; err != nil {
			log.Infof("ignore error: %v from %s for %s", err, f.addrs[resp.index], key)
		} else {
			log.Infof("ignore a slower result from %s for %s", f.addrs[resp.index], key)
		}
	}
}
