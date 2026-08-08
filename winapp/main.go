package main

import (
	"embed"
	"log"
	"os"

	"PrismPanel-winapp/internal/updater"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:assets
var assets embed.FS

var appVersion = "0.0.1"

func main() {
	if updater.IsApplyMode(os.Args[1:]) {
		if err := updater.Apply(os.Args[1:]); err != nil {
			updater.RecordFailure(err)
		}
		return
	}
	if executable, err := os.Executable(); err == nil {
		go updater.CleanupPrevious(executable)
	}
	app, err := newApp()
	if err != nil {
		log.Fatal(err)
	}
	if err := wails.Run(&options.App{
		Title:            "PrismPanel",
		Width:            1280,
		Height:           820,
		MinWidth:         960,
		MinHeight:        640,
		AssetServer:      &assetserver.Options{Assets: assets},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		Bind:             []interface{}{app},
		BackgroundColour: &options.RGBA{R: 245, G: 247, B: 246, A: 1},
	}); err != nil {
		log.Fatal(err)
	}
}
