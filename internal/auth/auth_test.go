package auth

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

func TestMain(m *testing.M) {
	log.SetOutput(io.Discard)
	os.Exit(m.Run())
}

func TestConfigTalksToThePublicClientEndpoints(t *testing.T) {
	conf := Config("http://localhost:8080", "http://localhost:5173", "http://127.0.0.1:4711/callback")

	assert.Equal(t, ClientID, conf.ClientID)
	assert.Empty(t, conf.ClientSecret, "the tracker is a public client")
	assert.Equal(t, oauth2.AuthStyleInParams, conf.Endpoint.AuthStyle, "client_id must travel in the form body, the server reads it there")
	assert.Equal(t, "http://localhost:5173/app/oauth/authorize", conf.Endpoint.AuthURL, "the browser goes to the app, which asks the player to approve")
	assert.Equal(t, "http://localhost:8080/oauth/token", conf.Endpoint.TokenURL, "the tracker trades the code with the API, not the app")
}

func TestConfigOnOneOrigin(t *testing.T) {
	conf := Config("https://atm.example", "https://atm.example", "http://127.0.0.1:4711/callback")

	assert.Equal(t, "https://atm.example/app/oauth/authorize", conf.Endpoint.AuthURL)
	assert.Equal(t, "https://atm.example/oauth/token", conf.Endpoint.TokenURL)
}

func TestCallbackHandlerChecksState(t *testing.T) {
	cases := []struct {
		name       string
		query      string
		wantErr    error
		wantCode   string
		wantStatus int
	}{
		{name: "the code comes back", query: "?state=sent&code=abc", wantCode: "abc", wantStatus: http.StatusOK},
		{name: "a state we did not send", query: "?state=other&code=abc", wantErr: errStateFailed, wantStatus: http.StatusBadRequest},
		{name: "no code at all", query: "?state=sent", wantErr: errNoCode, wantStatus: http.StatusBadRequest},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			results := make(chan callbackResult, 1)
			rec := httptest.NewRecorder()

			callbackHandler("sent", results).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/callback"+c.query, nil))

			result := <-results
			assert.ErrorIs(t, result.err, c.wantErr)
			assert.Equal(t, c.wantCode, result.code)
			assert.Equal(t, c.wantStatus, rec.Code)
		})
	}
}

func TestCallbackHandlerDeniedByThePlayer(t *testing.T) {
	results := make(chan callbackResult, 1)
	rec := httptest.NewRecorder()

	callbackHandler("sent", results).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/callback?state=sent&error=access_denied", nil))

	result := <-results
	require.Error(t, result.err, "a denied login must not look like a success")
	assert.Contains(t, result.err.Error(), "access_denied")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRandomStateIsNotReused(t *testing.T) {
	first, err := randomState()
	require.NoError(t, err)

	second, err := randomState()
	require.NoError(t, err)

	assert.NotEqual(t, first, second, "two logins would share a state")
	assert.GreaterOrEqual(t, len(first), 32, "a state this short is not worth checking")
}
