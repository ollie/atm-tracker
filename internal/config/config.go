// Package config is where the tracker looks for the API and the website.
package config

import (
	"os"

	"fyne.io/fyne/v2"
)

const (
	DefaultBaseURL = "https://atm.oldrichvetesnik.cz"

	baseURLEnv  = "ATM_API_URL"
	prefBaseURL = "apiBaseURL"

	appURLEnv  = "ATM_APP_URL"
	prefAppURL = "appBaseURL"
)

func BaseURL(prefs fyne.Preferences) string {
	if fromEnv := os.Getenv(baseURLEnv); fromEnv != "" {
		return fromEnv
	}

	return prefs.StringWithFallback(prefBaseURL, DefaultBaseURL)
}

func AppURL(prefs fyne.Preferences) string {
	if fromEnv := os.Getenv(appURLEnv); fromEnv != "" {
		return fromEnv
	}

	return prefs.StringWithFallback(prefAppURL, BaseURL(prefs))
}
