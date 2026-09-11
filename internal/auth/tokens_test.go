package auth

import (
	"errors"
	"testing"

	"fyne.io/fyne/v2/test"
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
