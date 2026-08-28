package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"PrismPanel-sessiond/internal/config"
	"PrismPanel-sessiond/internal/install"
	"PrismPanel-sessiond/internal/service"
)

func main() {
	configPath := flag.String("config", "", "path to sessiond YAML configuration")
	flag.Parse()
	switch flag.Arg(0) {
	case "install":
		if err := install.Install(*configPath); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("prism-sessiond service installed")
		return
	case "uninstall":
		if err := install.Uninstall(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("prism-sessiond service removed")
		return
	}
	cfg, err := config.LoadOrCreate(*configPath)
	if err != nil {
		slog.Error("load sessiond config", "error", err)
		os.Exit(1)
	}
	svc := service.New(cfg)
	go func() {
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
		<-signals
		svc.Close()
	}()
	slog.Info("prism-sessiond listening", "listen", cfg.Listen)
	if err := svc.ListenAndServe(); err != nil {
		slog.Error("sessiond stopped", "error", err)
		os.Exit(1)
	}
}
