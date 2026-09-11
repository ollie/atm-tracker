// Command atm-tracker streams X-Plane telemetry to Air Transport Magnate.
package main

import (
	"context"
	_ "embed"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"github.com/joho/godotenv"
	"github.com/ollie/atm-tracker/internal/tracker"
	"github.com/ollie/atm-tracker/internal/ui"
)

const (
	appID = "cz.oldrichvetesnik.atm"

	logFileName = "atm-tracker.log"
	envFileName = ".env"
	iconName    = "Icon.png"
)

//go:embed VERSION.txt
var versionRaw string

var version = strings.TrimSpace(versionRaw)

//go:embed Icon.png
var iconPNG []byte

func main() {
	_ = godotenv.Load(envFileName)

	// Ctrl+C, SIGTERM quits the app
	appCtx, appStop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer appStop()

	fyneApp := app.NewWithID(appID)
	fyneApp.SetIcon(fyne.NewStaticResource(iconName, iconPNG))

	var t *tracker.Tracker

	quit := func() {
		log.Println("quitting")
		log.SetOutput(os.Stderr)

		go func() {
			t.Shutdown()
			fyne.Do(fyneApp.Quit)
		}()
	}

	u := ui.New(fyneApp, ui.Actions{
		SignIn:  func() { t.SignIn() },
		SignOut: func() { t.SignOut() },
		Quit:    quit,
	})

	logFile := startLogging(u)
	if logFile != nil {
		defer func() { _ = logFile.Close() }()
	}

	apiUserAgent := "ATM Tracker v" + version
	t = tracker.New(fyneApp, u, apiUserAgent)
	t.Resume()

	go func() {
		<-appCtx.Done()
		quit()
	}()

	u.Win.ShowAndRun()
	log.Println("stopped")
}

func startLogging(u *ui.UI) *os.File {
	writers := []io.Writer{os.Stderr}

	file, path := openLogFile()
	if file != nil {
		writers = append(writers, file)
	}

	log.SetOutput(io.MultiWriter(writers...))
	u.SetLogFile(path)

	if path == "" {
		log.Printf("could not open %s, logging to screen only", logFileName)
	} else {
		log.Printf("logging to %s", path)
	}

	return file
}

func openLogFile() (*os.File, string) {
	path, err := filepath.Abs(logFileName)
	if err != nil {
		path = logFileName
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, ""
	}

	return file, path
}
