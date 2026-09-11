package config

import (
	"testing"

	"fyne.io/fyne/v2/test"
	"github.com/stretchr/testify/assert"
)

func TestBaseURL(t *testing.T) {
	cases := []struct {
		name  string
		env   string
		saved string
		want  string
	}{
		{name: "a fresh install talks to the live API", want: DefaultBaseURL},
		{name: "the player pointed it somewhere", saved: "https://atm.example", want: "https://atm.example"},
		{name: "the environment wins over the preference", env: "http://localhost:3000", saved: "https://atm.example", want: "http://localhost:3000"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.env != "" {
				t.Setenv(baseURLEnv, c.env)
			}

			prefs := test.NewApp().Preferences()
			if c.saved != "" {
				prefs.SetString(prefBaseURL, c.saved)
			}

			assert.Equal(t, c.want, BaseURL(prefs))
		})
	}
}

func TestAppURL(t *testing.T) {
	cases := []struct {
		name      string
		env       string
		saved     string
		savedBase string
		want      string
	}{
		{name: "one origin serves both, so the app follows the API", want: DefaultBaseURL},
		{name: "the API moved and the app came along", savedBase: "https://atm.example", want: "https://atm.example"},
		{name: "the player runs the app somewhere else", saved: "http://localhost:5173", savedBase: "http://localhost:8080", want: "http://localhost:5173"},
		{name: "the environment wins over the preference", env: "http://localhost:4173", saved: "http://localhost:5173", want: "http://localhost:4173"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.env != "" {
				t.Setenv(appURLEnv, c.env)
			}

			prefs := test.NewApp().Preferences()
			if c.saved != "" {
				prefs.SetString(prefAppURL, c.saved)
			}
			if c.savedBase != "" {
				prefs.SetString(prefBaseURL, c.savedBase)
			}

			assert.Equal(t, c.want, AppURL(prefs))
		})
	}
}
