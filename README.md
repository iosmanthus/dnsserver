# dnsserver

dnsserver is DNS proxy based on [CoreDNS](https://github.com/coredns/coredns). It forward the DNS request to different upstream by the rules. The rules are powered by the geosite data from V2Ray.

## Usage

The format of dnsserver config file is the based on `Corefile`, but with `v2router` plugin enabled and some other essential plugins:
- [x] `cancel`
- [x] `bind`
- [x] `prometheus`
- [x] `errors`
- [x] `cache`
- [x] `v2router`

Here is an example config for `v2router` plugin:
```Corefile
    # v2router [path of geosite.dat]
	v2router geosite.dat {
        # reject all ads domain
		reject geosite:category-ads-all

        # forward some category of domains to a list of upstream
        # 4 attributes available
	    # `connections`
	    # `retry`
	    # `timeout`
        # Only DNS over TCP/UDP are supported now.
		forward geosite:geolocation-!cn to tcp://1.1.1.1:53;tcp://8.8.8.8:53 retry:10 timeout:800ms
		forward geosite:cn to udp://114.114.114.114:53;udp://119.29.29.29:53

        # default route.
		default tcp://1.1.1.1:53;tcp://8.8.8.8:53 timeout:800ms retry:10
	}
```


These is also a Grafana panel available in `metrics/grafana/dnsserver.json`.