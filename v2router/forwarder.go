package v2router

import (
	"context"
	"fmt"
	"github.com/cenkalti/backoff"
	"github.com/iosmanthus/dnsserver/transport"
	"net"
	"time"

	"github.com/miekg/dns"
)

type Forwarder interface {
	fmt.Stringer
	Forward(ctx context.Context, w dns.ResponseWriter, m *dns.Msg) (int, error)
}

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
			Capacity:     int(attr.connections),
			Expire:       defaultTimeout,
			GC:           defaultTimeout,
			YieldTimeout: defaultTimeout,
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
	err      error
	ok       *dns.Msg
	upstream net.Addr
}

func getQuestionName(m *dns.Msg) string {
	var name = "UNKNOWN"
	if m != nil && len(m.Question) > 0 {
		name = m.Question[0].Name
	}
	return name
}

func (f *UpstreamsForwarder) doExchange(index int, m *dns.Msg) (*dns.Msg, error) {
	pc, cached, err := f.transports[index].Dial(f.timeout)
	if err != nil {
		return nil, err
	}
	r, _, err := f.clients[index].ExchangeWithConn(m, pc.Conn)
	if err != nil {
		go pc.Close()
		return nil, err
	}
	if cached {
		log.Infof("using cached connection for %s to %v", getQuestionName(m), f.addrs[index])
	}
	f.transports[index].Yield(pc)
	return r, nil
}

func (f *UpstreamsForwarder) exchange(ctx context.Context, index int, m *dns.Msg, ch chan<- result) {
	backoffer := backoff.WithMaxRetries(backoff.NewExponentialBackOff(), uint64(f.retry))
	boCtx := backoff.WithContext(backoffer, ctx)
	err := backoff.Retry(func() error {
		resp, err := f.doExchange(index, m)
		if err != nil {
			return err
		}
		ch <- result{
			ok:       resp,
			upstream: f.addrs[index],
		}
		return nil
	}, boCtx)
	if err != nil {
		ch <- result{
			err:      err,
			upstream: f.addrs[index],
		}
	}
}

func (f *UpstreamsForwarder) Forward(ctx context.Context, w dns.ResponseWriter, m *dns.Msg) (int, error) {
	responses := make(chan result, len(f.addrs))

	for i := range f.addrs {
		go f.exchange(ctx, i, m, responses)
	}
	select {
	case <-ctx.Done():
		return dns.RcodeServerFailure, ctx.Err()
	case resp := <-responses:
		if err := resp.err; err != nil {
			return dns.RcodeServerFailure, err
		}

		log.Infof("accepts response from %s for %s", resp.upstream, getQuestionName(m))
		upstreamGaugeVec.With(map[string]string{
			"upstream": resp.upstream.String(),
		}).Inc()

		w.WriteMsg(resp.ok)
		return resp.ok.Rcode, nil
	}
}

type Reject struct{}

func NewReject() Forwarder {
	return &Reject{}
}

func (f *Reject) String() string {
	return "reject"
}
func (f *Reject) Forward(_ context.Context, w dns.ResponseWriter, m *dns.Msg) (int, error) {
	name := getQuestionName(m)

	log.Infof("reject: %s", name)
	rejectGaugeVec.With(map[string]string{
		"name": name,
	}).Inc()

	empty := &dns.Msg{}
	empty.SetReply(m)
	empty.Answer = []dns.RR{
		&dns.A{A: net.ParseIP("0.0.0.0")},
	}
	w.WriteMsg(empty)
	return dns.RcodeSuccess, nil
}
