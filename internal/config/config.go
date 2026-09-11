// Package config is where the tracker looks for the game.
package config

import (
	"os"

	"fyne.io/fyne/v2"
)

const (
	DefaultBaseURL = "https://atm.oldrichvetesnik.cz"

	baseURLEnv  = "ATM_API_URL"
	prefBaseURL = "apiBaseURL"
)

func BaseURL(prefs fyne.Preferences) string {
	if fromEnv := os.Getenv("ATM_API_URL"); fromEnv != "" {
		return fromEnv
	}

	return prefs.StringWithFallback(prefBaseURL, DefaultBaseURL)
}
