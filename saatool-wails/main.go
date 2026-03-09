package main

import (
	"embed"
	"io"
	"log"
	"os"

	"github.com/dtylman/saatool/config"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// Load app options (creates config dir if needed)
	if err := config.LoadOptions(); err != nil {
		log.Printf("warning: could not load options: %v", err)
	}

	// Redirect log output so the in-app log view captures everything
	memLog := GetMemLogger()
	log.SetOutput(io.MultiWriter(os.Stderr, memLog))
	log.SetFlags(log.Ltime | log.Lshortfile)

	app := NewApp()

	err := wails.Run(&options.App{
		Title:  "SaaTool",
		Width:  1280,
		Height: 820,
		MinWidth:  900,
		MinHeight: 600,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 30, G: 30, B: 46, A: 255},
		OnStartup:        app.startup,
		Bind: []interface{}{
			app,
		},
	})
	if err != nil {
		log.Fatalf("wails run failed: %v", err)
	}
}
