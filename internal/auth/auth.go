// Package auth signs the player in to the game and keeps the token.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"fyne.io/fyne/v2"
	"golang.org/x/oauth2"
)

const (
	ClientID = "atm-tracker"

	ConsentPath = "/app/oauth/authorize"

	loopbackAddr      = "127.0.0.1:0" // `:0` will give us any port available.
	loginTimeout      = 5 * time.Minute
	readHeaderTimeout = 10 * time.Second
	stateBytes        = 32
)

var (
	errNoCode      = errors.New("the browser came back without an authorization code")
	errStateFailed = errors.New("the browser came back with a state we did not send")
)

func Config(apiBase, appBase, redirectURL string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:    ClientID,
		RedirectURL: redirectURL,
		Endpoint: oauth2.Endpoint{
			AuthURL:   appBase + ConsentPath,
			TokenURL:  apiBase + "/api/v1/oauth/token",
			AuthStyle: oauth2.AuthStyleInParams,
		},
	}
}

type callbackResult struct {
	code string
	err  error
}

func Login(ctx context.Context, app fyne.App, apiBase, appBase string) (*oauth2.Token, error) {
	var config net.ListenConfig

	listener, err := config.Listen(ctx, "tcp", loopbackAddr)
	if err != nil {
		return nil, fmt.Errorf("listen for the browser: %w", err)
	}
	defer func() { _ = listener.Close() }()

	state, err := randomState()
	if err != nil {
		return nil, err
	}

	conf := Config(apiBase, appBase, "http://"+listener.Addr().String()+"/callback")
	verifier := oauth2.GenerateVerifier()

	results := make(chan callbackResult, 1)
	server := &http.Server{Handler: callbackHandler(state, results), ReadHeaderTimeout: readHeaderTimeout}
	go func() { _ = server.Serve(listener) }()
	defer func() { _ = server.Close() }()

	authURL, err := url.Parse(conf.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier)))
	if err != nil {
		return nil, fmt.Errorf("build the authorization URL: %w", err)
	}
	if err := app.OpenURL(authURL); err != nil {
		return nil, fmt.Errorf("open the browser: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, loginTimeout)
	defer cancel()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-results:
		if result.err != nil {
			return nil, result.err
		}
		return conf.Exchange(ctx, result.code, oauth2.VerifierOption(verifier))
	}
}

func callbackHandler(state string, results chan<- callbackResult) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()

		var result callbackResult
		switch {
		case query.Get("state") != state:
			result.err = errStateFailed
		case query.Get("error") != "":
			result.err = fmt.Errorf("the game refused the login: %s", query.Get("error"))
		case query.Get("code") == "":
			result.err = errNoCode
		default:
			result.code = query.Get("code")
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if result.err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, callbackPage("Not signed in", result.err.Error()))
		} else {
			_, _ = io.WriteString(w, callbackPage("Signed in", "You can close this window and go back to the tracker."))
		}

		select {
		case results <- result:
		default:
		}
	})
}

func callbackPage(heading, message string) string {
	return fmt.Sprintf(`<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>%s — ATM Tracker</title></head>
<body style="font-family: system-ui, sans-serif; margin: 4rem auto; max-width: 30rem">
<h1>%s</h1>
<p>%s</p>
</body>
</html>
`, heading, heading, message)
}

func randomState() (string, error) {
	buf := make([]byte, stateBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(buf), nil
}
