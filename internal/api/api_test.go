package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"golang.org/x/oauth2"
)

func TestErrorReason(t *testing.T) {
	cases := []struct {
		name string
		body errorResponse
		want string
	}{
		{
			name: "field code wins over the envelope",
			body: errorResponse{Code: "validation_failed", Fields: map[string]fieldError{"base": {Code: "flight_not_active"}}},
			want: "flight_not_active",
		},
		{
			name: "falls back to the envelope",
			body: errorResponse{Code: "unauthorized"},
			want: "unauthorized",
		},
		{
			name: "empty when the body says nothing",
			want: "",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := &Error{status: http.StatusUnprocessableEntity, body: c.body}

			assert.Equal(t, c.want, err.reason())
		})
	}
}

func TestFailedWithSeesThroughWrapping(t *testing.T) {
	err := fmt.Errorf("request: %w", &Error{
		status: http.StatusUnprocessableEntity,
		body:   errorResponse{Code: "validation_failed", Fields: map[string]fieldError{"base": {Code: "flight_not_active"}}},
	})

	assert.True(t, FailedWith(err, "flight_not_active"))
	assert.False(t, FailedWith(err, "something_else"))
	assert.False(t, FailedWith(errors.New("plain"), "flight_not_active"))
}

func TestSignedOut(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nothing went wrong", err: nil, want: false},
		{name: "the bearer was refused", err: fmt.Errorf("request: %w", &Error{status: http.StatusUnauthorized}), want: true},
		{name: "the flight was refused", err: &Error{status: http.StatusUnprocessableEntity}, want: false},
		{
			name: "the refresh token was refused",
			err:  &url.Error{Op: "Get", Err: &oauth2.RetrieveError{Response: &http.Response{StatusCode: http.StatusBadRequest}}},
			want: true,
		},
		{name: "the game is unreachable", err: &url.Error{Op: "Get", Err: errors.New("connection refused")}, want: false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, SignedOut(c.err))
		})
	}
}
