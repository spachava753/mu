package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/spachava753/mu/internal/app"
	"github.com/spachava753/mu/internal/desktop"
	"github.com/spachava753/mu/internal/state"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "mu:", err)
		os.Exit(1)
	}
}

func run() (err error) {
	dataDir := flag.String("data-dir", "", "session reference directory (default: $XDG_DATA_HOME/mu or ~/.local/share/mu)")
	command := flag.String("agent", "", "prefill the new-thread ACP command")
	flag.Parse()
	if *dataDir == "" {
		*dataDir, err = state.DefaultDir()
		if err != nil {
			return err
		}
	}
	workDir, err := state.DefaultWorkingDir()
	if err != nil {
		return err
	}
	a, err := app.Open(*dataDir, workDir)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := a.Close(); err == nil {
			err = closeErr
		}
	}()
	view := desktop.New(a, *command)
	mygo.App.SetName("Mu")
	mygo.App.WhenReady(func() {
		w := mygo.NewWindow(mygo.WindowOptions{Title: "Mu", Width: 1100, Height: 780, MinWidth: 800, MinHeight: 600, Content: ui.View(view.Draw)})
		done := make(chan struct{})
		w.OnClosed(func() { close(done) })
		go func() {
			for {
				select {
				case <-done:
					return
				case <-a.Changes():
					w.Invalidate()
				}
			}
		}()
	})
	return mygo.App.Run()
}
