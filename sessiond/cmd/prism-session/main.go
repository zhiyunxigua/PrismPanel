package main

import (
	"flag"
	"fmt"
	"os"

	"PrismPanel-sessiond/internal/client"
	"PrismPanel-sessiond/internal/config"
	"PrismPanel-sessiond/internal/tui"
)

func main() {
	configPath := flag.String("config", "", "path to sessiond YAML configuration")
	flag.Parse()
	cfg, err := config.LoadOrCreate(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	manager, err := client.Dial(cfg.Listen, cfg.Token, 0)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer manager.Close()
	switch flag.Arg(0) {
	case "list":
		sessions, err := manager.List()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		for _, item := range sessions {
			fmt.Println(client.FormatState(item))
		}
	default:
		if err := tui.Run(manager); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}
