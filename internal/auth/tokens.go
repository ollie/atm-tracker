package auth

import (
	"context"
	"encoding/json"
	"log"
	"sync"

	"fyne.io/fyne/v2"
	"golang.org/x/oauth2"
)

const prefToken = "oauthToken"

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

func (s *Store) Source(ctx context.Context, base string, token *oauth2.Token) oauth2.TokenSource {
	return &savingTokenSource{
		store: s,
		inner: Config(base, "").TokenSource(ctx, token),
	}
}

type savingTokenSource struct {
	store *Store
	inner oauth2.TokenSource
}

func (s *savingTokenSource) Token() (*oauth2.Token, error) {
	token, err := s.inner.Token()
	if err != nil {
		return nil, err
	}

	s.store.mu.Lock()
	rotated := token.RefreshToken != s.store.current
	s.store.mu.Unlock()

	if rotated {
		s.store.Save(token)
	}

	return token, nil
}
