package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app := NewApp()

	err := wails.Run(&options.App{
		Title:             "OneBoard Safe Paste",
		Width:             1280,
		Height:            800,
		MinWidth:          1024,
		MinHeight:         720,
		Frameless:         true,
		HideWindowOnClose: false,
		OnBeforeClose:     app.BeforeClose,
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			DisableWindowIcon:    false,
		},
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 13, G: 17, B: 23, A: 1},
		OnStartup:        app.startup,
		Bind:             []interface{}{app},
	})
	if err != nil {
		log.Fatal(err)
	}
}
