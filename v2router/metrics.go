package v2router

import "github.com/prometheus/client_golang/prometheus"

func init() {
	prometheus.MustRegister(rejectGaugeVec)
	prometheus.MustRegister(upstreamGaugeVec)
}

var (
	rejectGaugeVec = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "dnsserver",
		Subsystem: "v2router",
		Name:      "reject",
		Help:      "A metric records the rejected times of a domain",
	}, []string{"name"})

	upstreamGaugeVec = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "dnsserver",
		Subsystem: "v2router",
		Name:      "upstream",
		Help:      "A metric records the upstream of requests",
	}, []string{"upstream"})
)
