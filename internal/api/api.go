// Package api asks the ATM API what to fly and tells it what happened.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/ollie/atm-tracker/internal/telemetry"
	"golang.org/x/oauth2"
	"resty.dev/v3"
)

const requestTimeout = 10 * time.Second

type AirportInfo struct {
	Ident string `json:"ident"`
	Name  string `json:"name"`
}

type FlightInfo struct {
	ID              int         `json:"id"`
	Status          string      `json:"status"`
	Departure       AirportInfo `json:"departure"`
	Arrival         AirportInfo `json:"arrival"`
	TargetPayloadKg int         `json:"targetPayloadKg"`
	TargetFuelKg    int         `json:"targetFuelKg"`
	WeightUnit      string      `json:"weightUnit"`
}

type Event struct {
	Kind       string            `json:"kind"`
	Params     map[string]string `json:"params"`
	OccurredAt time.Time         `json:"occurredAt"`
}

type PositionsRequest struct {
	FlightID  int                  `json:"flightId"`
	Positions []telemetry.Position `json:"positions"`
}

type PositionsResult struct {
	Status string  `json:"status"`
	Events []Event `json:"events"`
}

type fieldError struct {
	Code string `json:"code"`
}

type errorResponse struct {
	Code   string                `json:"code"`
	Fields map[string]fieldError `json:"fields"`
}

type Error struct {
	status int
	body   errorResponse
}

func (e *Error) Error() string {
	if e.body.Code == "" {
		return fmt.Sprintf("server returned %d", e.status)
	}

	return fmt.Sprintf("server returned %d: %s", e.status, e.reason())
}

func (e *Error) reason() string {
	for _, field := range e.body.Fields {
		if field.Code != "" {
			return field.Code
		}
	}

	return e.body.Code
}

func newError(res *resty.Response) *Error {
	err := &Error{status: res.StatusCode()}
	_ = json.Unmarshal(res.Bytes(), &err.body)
	return err
}

func FailedWith(err error, reason string) bool {
	var apiErr *Error
	return errors.As(err, &apiErr) && apiErr.reason() == reason
}

func SignedOut(err error) bool {
	var apiErr *Error
	if errors.As(err, &apiErr) && apiErr.status == http.StatusUnauthorized {
		return true
	}

	var retrieveErr *oauth2.RetrieveError
	return errors.As(err, &retrieveErr)
}

type Client struct {
	rest *resty.Client
}

func New(base, userAgent string, tokens oauth2.TokenSource) *Client {
	return &Client{
		rest: resty.New().
			SetBaseURL(base+"/api/v1").
			SetHeader("User-Agent", userAgent).
			SetHeader("Content-Type", "application/json").
			SetTimeout(requestTimeout).
			SetTransport(&oauth2.Transport{Source: tokens}),
	}
}

func (c *Client) Close() {
	_ = c.rest.Close()
}

func (c *Client) Flight(ctx context.Context) (*FlightInfo, error) {
	var item FlightInfo

	res, err := c.rest.R().SetContext(ctx).SetResult(&item).Get("/tracker/flight")
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	if res.StatusCode() == http.StatusNoContent {
		return nil, nil
	}
	if res.IsStatusFailure() {
		return nil, newError(res)
	}

	return &item, nil
}

func (c *Client) SendPositions(ctx context.Context, flightID int, batch []telemetry.Position) (*PositionsResult, error) {
	var result PositionsResult

	res, err := c.rest.R().
		SetContext(ctx).
		SetBody(PositionsRequest{FlightID: flightID, Positions: batch}).
		SetResult(&result).
		Post("/tracker/positions")
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	if res.IsStatusFailure() {
		return nil, newError(res)
	}

	return &result, nil
}

func (c *Client) Revoke(ctx context.Context) error {
	res, err := c.rest.R().SetContext(ctx).Post("/oauth/revoke")
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	if res.IsStatusFailure() {
		return newError(res)
	}

	return nil
}
