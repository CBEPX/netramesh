package main

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"

	"github.com/opentracing/opentracing-go"
	"github.com/patrickmn/go-cache"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	jaegercfg "github.com/uber/jaeger-client-go/config"

	"github.com/Lookyan/netramesh/internal/config"
	"github.com/Lookyan/netramesh/pkg/estabcache"
	"github.com/Lookyan/netramesh/pkg/log"
	"github.com/Lookyan/netramesh/pkg/protocol"
	"github.com/Lookyan/netramesh/pkg/transport"
)

func main() {
	logger, err := log.Init("NETRA", os.Getenv(log.EnvNetraLoggerLevel), os.Stderr)
	if err != nil {
		log.Fatal(err.Error())
	}
	serviceName := flag.String("service-name", "", "service name for jaeger")
	flag.Parse()
	if *serviceName == "" {
		logger.Fatal("service-name flag should be set")
	}
	config.SetServiceName(*serviceName)

	err = config.GlobalConfigFromENV(logger)
	if err != nil {
		logger.Fatal(err.Error())
	}

	go func() {
		// pprof
		logger.Error(
			http.ListenAndServe(
				fmt.Sprintf("0.0.0.0:%d", config.GetNetraConfig().PprofPort), nil))
	}()
	go func() {
		// prometheus
		logger.Error(
			http.ListenAndServe(
				fmt.Sprintf("0.0.0.0:%d", config.GetNetraConfig().PrometheusPort), promhttp.Handler()))
	}()

	os.Setenv("JAEGER_SERVICE_NAME", *serviceName)
	cfg, err := jaegercfg.FromEnv()
	if err != nil {
		// parsing errors might happen here, such as when we get a string where we expect a number
		logger.Fatalf("Could not parse Jaeger env vars: %s", err.Error())
	}
	tracer, closer, err := cfg.NewTracer()
	if err != nil {
		logger.Fatalf("Could not initialize jaeger tracer: %s", err.Error())
	}
	defer closer.Close()
	opentracing.SetGlobalTracer(tracer)

	addr := fmt.Sprintf("0.0.0.0:%d", config.GetNetraConfig().Port)
	lAddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		logger.Fatal(err.Error())
	}

	ln, err := net.ListenTCP("tcp", lAddr)
	if err != nil {
		logger.Fatal(err.Error())
	}

	establishedCache := estabcache.NewEstablishedCache()

	tracingContextMapping := cache.New(
		config.GetNetraConfig().TracingContextExpiration,
		config.GetNetraConfig().TracingContextCleanupInterval,
	)

	routingInfoContextMapping := cache.New(
		config.GetNetraConfig().RoutingContextExpiration,
		config.GetNetraConfig().RoutingContextCleanupInterval,
	)

	protocol.InitHandlerRequest(logger, tracingContextMapping, routingInfoContextMapping)

	for {
		conn, err := ln.AcceptTCP()
		if err != nil {
			logger.Warning(err.Error())
			continue
		}
		go transport.HandleConnection(
			logger,
			conn,
			establishedCache,
			tracingContextMapping,
			routingInfoContextMapping)
	}
}
