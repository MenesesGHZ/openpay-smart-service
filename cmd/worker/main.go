package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/menesesghz/openpay-smart-service/internal/config"
)

var cfgPath = flag.String("config", "config.yaml", "path to config file")

func main() {
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	level, _ := zerolog.ParseLevel(cfg.Telemetry.LogLevel)
	zerolog.SetGlobalLevel(level)
	log.Logger = zerolog.New(os.Stdout).With().
		Timestamp().
		Str("service", cfg.Telemetry.ServiceName+"-worker").
		Logger()

	log.Info().Msg("starting kafka worker")

	ctx, cancel := context.WithCancel(context.Background())

	// TODO: start webhook dispatcher consumer
	// TODO: start disbursement scheduler consumer
	_ = cfg // used once wired

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info().Msg("shutting down worker...")
	cancel()
	_ = ctx

	log.Info().Msg("worker stopped")
}
