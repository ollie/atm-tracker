package auth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"github.com/ollie/atm-tracker/internal/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

func newStore(t *testing.T) *Store {
	t.Helper()

	return &Store{Prefs: test.NewApp().Preferences()}
}

type fixedSource struct {
	token *oauth2.Token
	err   error
}

func (f *fixedSource) Token() (*oauth2.Token, error) {
	return f.token, f.err
}

func TestStoreRoundTripsTheToken(t *testing.T) {
	s := newStore(t)
	assert.Nil(t, s.Load(), "a fresh install is signed out")

	s.Save(&oauth2.Token{AccessToken: "access", RefreshToken: "refresh", TokenType: "Bearer"})

	loaded := s.Load()
	require.NotNil(t, loaded)
	assert.Equal(t, "access", loaded.AccessToken)
	assert.Equal(t, "refresh", loaded.RefreshToken)

	s.Clear()
	assert.Nil(t, s.Load(), "signing out left the token behind")
}

func TestStoreForgetsATokenItCannotRead(t *testing.T) {
	s := newStore(t)
	s.Prefs.SetString(prefToken, "{not json")

	assert.Nil(t, s.Load())
	assert.Empty(t, s.Prefs.String(prefToken), "an unreadable token must be dropped, not read again every launch")
}

func TestSavingTokenSourceStoresARotatedRefreshToken(t *testing.T) {
	s := newStore(t)
	s.Save(&oauth2.Token{AccessToken: "old", RefreshToken: "first"})

	source := &savingTokenSource{store: s, inner: &fixedSource{token: &oauth2.Token{AccessToken: "new", RefreshToken: "second"}}}

	token, err := source.Token()
	require.NoError(t, err)
	assert.Equal(t, "new", token.AccessToken)

	stored := s.Load()
	require.NotNil(t, stored)
	assert.Equal(t, "second", stored.RefreshToken, "the old refresh token is dead once the server rotates it")
}

func TestSavingTokenSourceLeavesAnUnchangedTokenAlone(t *testing.T) {
	s := newStore(t)
	s.Save(&oauth2.Token{AccessToken: "old", RefreshToken: "same"})
	s.Prefs.SetString(prefToken, "sentinel")

	source := &savingTokenSource{store: s, inner: &fixedSource{token: &oauth2.Token{AccessToken: "new", RefreshToken: "same"}}}

	_, err := source.Token()
	require.NoError(t, err)

	assert.Equal(t, "sentinel", s.Prefs.String(prefToken), "an unrotated refresh token must not cost a write")
}

func TestSavingTokenSourcePassesTheRefusalOn(t *testing.T) {
	s := newStore(t)
	refused := errors.New("refresh refused")

	source := &savingTokenSource{store: s, inner: &fixedSource{err: refused}}

	_, err := source.Token()
	assert.ErrorIs(t, err, refused)
}

type tokenEndpoint struct {
	URL      string
	refusals func(w http.ResponseWriter) bool
	calls    atomic.Int32
}

func newTokenEndpoint(t *testing.T) *tokenEndpoint {
	t.Helper()

	e := &tokenEndpoint{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+TokenPath, func(w http.ResponseWriter, _ *http.Request) {
		e.calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if e.refusals != nil && e.refusals(w) {
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"fresh","token_type":"Bearer","refresh_token":"second","expires_in":7200}`))
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	e.URL = server.URL

	return e
}

func storedToken(t *testing.T, s *Store, expiresIn time.Duration) *oauth2.Token {
	t.Helper()

	token := &oauth2.Token{AccessToken: "old", RefreshToken: "first", TokenType: "Bearer", Expiry: time.Now().Add(expiresIn)}
	s.Save(token)

	return token
}

func TestSourceRefreshesAMinuteBeforeTheTokenExpires(t *testing.T) {
	s := newStore(t)
	endpoint := newTokenEndpoint(t)

	source := s.Source(t.Context(), endpoint.URL, storedToken(t, s, 30*time.Second))

	token, err := source.Token()
	require.NoError(t, err)
	assert.Equal(t, "fresh", token.AccessToken)
	assert.Equal(t, int32(1), endpoint.calls.Load())
	assert.Equal(t, "second", s.Load().RefreshToken, "the rotated refresh token is stored")
}

func TestSourceKeepsATokenWithMoreThanAMinuteLeft(t *testing.T) {
	s := newStore(t)
	endpoint := newTokenEndpoint(t)

	source := s.Source(t.Context(), endpoint.URL, storedToken(t, s, 2*time.Minute))

	token, err := source.Token()
	require.NoError(t, err)
	assert.Equal(t, "old", token.AccessToken)
	assert.Equal(t, int32(0), endpoint.calls.Load())
}

func TestExpireForcesARefreshOnTheNextToken(t *testing.T) {
	s := newStore(t)
	endpoint := newTokenEndpoint(t)

	source := s.Source(t.Context(), endpoint.URL, storedToken(t, s, time.Hour))

	token, err := source.Token()
	require.NoError(t, err)
	assert.Equal(t, "old", token.AccessToken)

	source.Expire()

	token, err = source.Token()
	require.NoError(t, err)
	assert.Equal(t, "fresh", token.AccessToken)
	assert.Equal(t, int32(1), endpoint.calls.Load())
	assert.Equal(t, "second", s.Load().RefreshToken)

	token, err = source.Token()
	require.NoError(t, err)
	assert.Equal(t, "fresh", token.AccessToken)
	assert.Equal(t, int32(1), endpoint.calls.Load(), "the fresh token is reused until it nears expiry")
}

func TestARefusedRefreshTokenReadsAsSignedOut(t *testing.T) {
	s := newStore(t)
	endpoint := newTokenEndpoint(t)
	endpoint.refusals = func(w http.ResponseWriter) bool {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"refresh token is invalid"}`))
		return true
	}

	source := s.Source(t.Context(), endpoint.URL, storedToken(t, s, 30*time.Second))

	_, err := source.Token()
	assert.True(t, api.SignedOut(err))
}

func TestATokenEndpointOutageIsNotASignOut(t *testing.T) {
	s := newStore(t)
	endpoint := newTokenEndpoint(t)
	endpoint.refusals = func(w http.ResponseWriter) bool {
		w.WriteHeader(http.StatusServiceUnavailable)
		return true
	}

	source := s.Source(t.Context(), endpoint.URL, storedToken(t, s, 30*time.Second))

	_, err := source.Token()
	require.Error(t, err)
	assert.False(t, api.SignedOut(err), "a server hiccup must not throw the player's refresh token away")
	assert.Equal(t, "first", s.Load().RefreshToken)
}
