package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"syscall"

	"github.com/aaron/fcomt-agent/internal/agent"
	"github.com/aaron/fcomt-agent/internal/config"
)

func main() {
	var (
		configPath = flag.String("config", "", "path to YAML config file")
		listenAddr = flag.String("listen", "", "override API listen address")
	)
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if *listenAddr != "" {
		cfg.API.Listen = *listenAddr
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	app, err := agent.New(cfg)
	if err != nil {
		log.Fatalf("create app: %v", err)
	}

	log.Printf("starting FComT agent: profile=%s api=%s", cfg.Monitoring.Profile, cfg.API.Listen)
	if err := app.Run(ctx); err != nil {
		log.Fatalf("run app: %v", err)
	}
}

func loadConfig(path string) (config.AppConfig, error) {
	if path == "" {
		cfg := config.Default()
		return cfg, cfg.Validate()
	}
	return config.Load(path)
}
