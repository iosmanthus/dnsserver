package main

import (
	_ "github.com/coredns/coredns/plugin/bind"
	_ "github.com/coredns/coredns/plugin/cache"
	_ "github.com/coredns/coredns/plugin/cancel"
	_ "github.com/coredns/coredns/plugin/errors"
	_ "github.com/coredns/coredns/plugin/metrics"
	_ "github.com/iosmanthus/dnsserver/v2router"

	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/coremain"
)

var directives = []string{
	"cancel",
	"bind",
	"prometheus",
	"errors",
	"cache",
	"v2router",
}

func init() {
	dnsserver.Directives = directives
}

func main() {
	coremain.Run()
}
