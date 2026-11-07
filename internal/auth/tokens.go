package auth

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"github.com/ollie/atm-tracker/internal/api"
	"golang.org/x/oauth2"
)

const (
	prefToken           = "oauthToken"
	refreshBeforeExpiry = time.Minute
)

type Store struct {
	Prefs fyne.Preferences

	mu      sync.Mutex
	current string
}

func (s *Store) Load() *oauth2.Token {
	stored := s.Prefs.String(prefToken)
	if stored == "" {
		return nil
	}

	var token oauth2.Token
	if err := json.Unmarshal([]byte(stored), &token); err != nil {
		log.Printf("stored token unreadable, signing out: %v", err)
		s.Clear()
		return nil
	}

	s.mu.Lock()
	s.current = token.RefreshToken
	s.mu.Unlock()

	return &token
}

func (s *Store) Save(token *oauth2.Token) {
	stored, err := json.Marshal(token)
	if err != nil {
		log.Printf("could not store token: %v", err)
		return
	}

	s.mu.Lock()
	s.current = token.RefreshToken
	s.mu.Unlock()

	s.Prefs.SetString(prefToken, string(stored))
}

func (s *Store) Clear() {
	s.mu.Lock()
	s.current = ""
	s.mu.Unlock()

	s.Prefs.RemoveValue(prefToken)
}

func (s *Store) Source(ctx context.Context, base string, token *oauth2.Token) api.TokenSource {
	renew := func(token *oauth2.Token) oauth2.TokenSource {
		return oauth2.ReuseTokenSourceWithExpiry(token, Config(base, "", "").TokenSource(ctx, token), refreshBeforeExpiry)
	}

	return &savingTokenSource{
		store:     s,
		renew:     renew,
		inner:     renew(token),
		lastToken: token,
	}
}

type savingTokenSource struct {
	store *Store
	renew func(token *oauth2.Token) oauth2.TokenSource

	mu        sync.Mutex
	inner     oauth2.TokenSource
	lastToken *oauth2.Token
}

func (s *savingTokenSource) Token() (*oauth2.Token, error) {
	s.mu.Lock()
	inner := s.inner
	s.mu.Unlock()

	token, err := inner.Token()
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.lastToken = token
	s.mu.Unlock()

	s.store.mu.Lock()
	rotated := token.RefreshToken != s.store.current
	s.store.mu.Unlock()

	if rotated {
		s.store.Save(token)
	}

	return token, nil
}

func (s *savingTokenSource) Expire() {
	s.mu.Lock()
	defer s.mu.Unlock()

	expiredToken := *s.lastToken
	expiredToken.AccessToken = ""
	s.inner = s.renew(&expiredToken)
}
