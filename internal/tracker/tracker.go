// Package tracker signs in, finds the flight, and keeps the sim and the game talking.
package tracker

import (
	"context"
	"log"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"github.com/ollie/atm-tracker/internal/api"
	"github.com/ollie/atm-tracker/internal/auth"
	"github.com/ollie/atm-tracker/internal/config"
	"github.com/ollie/atm-tracker/internal/xplane"
	"golang.org/x/oauth2"
)

const (
	sendEvery       = 10 * time.Second
	flightPollEvery = 5 * time.Second
	revokeTimeout   = 10 * time.Second
	shutdownGrace   = 5 * time.Second
)

type Display interface {
	SigningIn()
	SignedIn()
	SignedOut()
	SetNoFlight()
	StartFlight(item *api.FlightInfo)
	SetFlight(item *api.FlightInfo)
	SetLive(live bool)
	SetNote(note string)
	SetProgress(result *api.PositionsResult)
}

type Timings struct {
	Send       time.Duration
	FlightPoll time.Duration
}

func DefaultTimings() Timings {
	return Timings{
		Send:       sendEvery,
		FlightPoll: flightPollEvery,
	}
}

type Tracker struct {
	app       fyne.App
	ui        Display
	base      string
	userAgent string
	store     *auth.Store

	mu      sync.Mutex
	stop    context.CancelFunc
	current *api.Client
	wg      sync.WaitGroup
}

func New(app fyne.App, u Display, userAgent string) *Tracker {
	return &Tracker{
		app:       app,
		ui:        u,
		base:      config.BaseURL(app.Preferences()),
		userAgent: userAgent,
		store:     &auth.Store{Prefs: app.Preferences()},
	}
}

func (t *Tracker) Resume() {
	log.Printf("talking to %s", t.base)

	token := t.store.Load()
	if token == nil {
		t.ui.SignedOut()
		return
	}

	t.startSession(token)
}

func (t *Tracker) SignIn() {
	t.ui.SigningIn()

	go func() {
		token, err := auth.Login(context.Background(), t.app, t.base)
		if err != nil {
			log.Printf("log in failed: %v", err)
			t.ui.SignedOut()
			t.ui.SetNote("log in failed, see the log")
			return
		}

		t.store.Save(token)
		t.startSession(token)
	}()
}

func (t *Tracker) SignOut() {
	t.mu.Lock()
	client := t.current
	t.mu.Unlock()

	go func() {
		if client != nil {
			ctx, cancel := context.WithTimeout(context.Background(), revokeTimeout)
			defer cancel()

			if err := client.Revoke(ctx); err != nil {
				log.Printf("could not revoke the token: %v", err)
			}
		}

		t.endSession()
	}()
}

func (t *Tracker) startSession(token *oauth2.Token) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.stop != nil {
		log.Print("already signed in, keeping the session that is running")
		return
	}

	ctx, stop := context.WithCancel(context.Background())
	client := api.New(t.base, t.userAgent, t.store.Source(ctx, t.base, token))
	t.stop, t.current = stop, client
	t.ui.SignedIn()

	s := &session{
		api:         client,
		ui:          t.ui,
		onSignedOut: t.endSession,
		simAddr:     xplane.Addr,
		timings:     DefaultTimings(),
		simTimings:  xplane.DefaultTimings(),
	}

	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		defer client.Close()
		s.run(ctx)
	}()
}

func (t *Tracker) endSession() {
	t.mu.Lock()
	stop := t.stop
	t.stop, t.current = nil, nil
	t.mu.Unlock()

	if stop != nil {
		stop()
	}

	t.store.Clear()
	t.ui.SignedOut()
}

func (t *Tracker) Shutdown() {
	t.mu.Lock()
	stop := t.stop
	t.stop, t.current = nil, nil
	t.mu.Unlock()

	if stop != nil {
		stop()
	}

	if !waitFor(&t.wg, shutdownGrace) {
		log.Printf("gave up after %s waiting for tracker goroutines", shutdownGrace)
	}
}

func logError(err error) {
	if err != nil {
		log.Printf("%v", err)
	}
}

func waitFor(wg *sync.WaitGroup, timeout time.Duration) bool {
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}
