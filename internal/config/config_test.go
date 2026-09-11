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
		{name: "a fresh install talks to the live game", want: DefaultBaseURL},
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
