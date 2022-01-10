package transport

import "github.com/prometheus/client_golang/prometheus"

func init() {
	prometheus.MustRegister(dialHistogramVec)
}

var (
	dialHistogramVec = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "dnsserver",
		Subsystem: "v2router",
		Name:      "dial",
		Help:      "A metric records the duration of DNS connection dialing",
	}, []string{"address"})
)
