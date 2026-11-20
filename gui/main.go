package main

import (
	"embed"
	"fmt"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

// version is injected at release time:
// -ldflags "-X main.version=v1.2.3".
var version = "dev"

func main() {
	app := NewApp()
	err := wails.Run(&options.App{
		Title:     fmt.Sprintf("monctl %s", version),
		Width:     440,
		Height:    680,
		MinWidth:  380,
		MinHeight: 420,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 24, G: 26, B: 31, A: 1},
		OnStartup:        app.startup,
		Bind:             []interface{}{app},
	})
	if err != nil {
		panic(err)
	}
}
