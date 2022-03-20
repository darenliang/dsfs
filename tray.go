package main

import (
	"github.com/darenliang/dsfs/icon"
	"github.com/getlantern/systray"
	"os"
)

func onReady() {
	systray.SetIcon(icon.Data)
	systray.SetTitle("dsfs")
	systray.SetTooltip("Use Discord as a Filesystem")
	mQuit := systray.AddMenuItem("Quit", "Quit dsfs")
	go func() {
		<-mQuit.ClickedCh
		systray.Quit()
	}()
}

func onExit() {
	os.Exit(0)
}
