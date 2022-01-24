package main

import (
	"os"

	_ "github.com/coredns/coredns/plugin/bind"
	_ "github.com/coredns/coredns/plugin/cache"
	_ "github.com/coredns/coredns/plugin/cancel"
	_ "github.com/coredns/coredns/plugin/errors"
	_ "github.com/coredns/coredns/plugin/metrics"
	_ "github.com/iosmanthus/dnsserver/v2router"

	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/coremain"

	"github.com/caarlos0/env/v6"
	log "github.com/sirupsen/logrus"
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

type config struct {
	LogLevel string `env:"LOG_LEVEL" envDefault:"info"`
}

func main() {
	log.SetOutput(os.Stdout)
	log.SetFormatter(&log.TextFormatter{
		DisableLevelTruncation: true,
		FullTimestamp:          true,
		ForceColors:            true,
	})

	c := &config{}
	err := env.Parse(c)
	if err != nil {
		log.Fatal(err)
	}

	level, err := log.ParseLevel(c.LogLevel)
	if err != nil {
		log.Fatal(err)
	}
	log.SetLevel(level)
	coremain.Run()
}
