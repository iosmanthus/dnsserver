package v2router

import (
	"context"
	"github.com/iosmanthus/dnsserver/request"
	"net"

	"github.com/miekg/dns"
)

type Reject struct{}

func NewReject() Forwarder {
	return &Reject{}
}

func (f *Reject) String() string {
	return "reject"
}
func (f *Reject) Forward(ctx context.Context, w dns.ResponseWriter, m *dns.Msg) (int, error) {
	log := request.WithLogger(ctx, log)
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
	_ = w.WriteMsg(empty)
	return dns.RcodeSuccess, nil
}
