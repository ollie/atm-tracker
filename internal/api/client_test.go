package api_test

import (
	"context"
	"io"
	"log"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/ollie/atm-tracker/internal/api"
	"github.com/ollie/atm-tracker/internal/api/apitest"
	"github.com/ollie/atm-tracker/internal/telemetry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const userAgent = "ATM Tracker test"

func TestMain(m *testing.M) {
	log.SetOutput(io.Discard)
	os.Exit(m.Run())
}

func newClient(t *testing.T, server *apitest.Server) *api.Client {
	t.Helper()

	client := api.New(server.URL, userAgent, apitest.StaticToken())
	t.Cleanup(client.Close)

	return client
}

func TestFlightReadsTheCurrentFlight(t *testing.T) {
	server := apitest.NewServer(t)
	server.SetFlight(&api.FlightInfo{
		ID:              42,
		Status:          "boarding",
		Departure:       api.AirportInfo{Ident: "LKPR", Name: "Prague"},
		Arrival:         api.AirportInfo{Ident: "EGLL", Name: "Heathrow"},
		TargetPayloadKg: 1200,
		TargetFuelKg:    800,
	})

	item, err := newClient(t, server).Flight(t.Context())
	require.NoError(t, err)
	require.NotNil(t, item)

	assert.Equal(t, 42, item.ID)
	assert.Equal(t, "LKPR", item.Departure.Ident)
	assert.Equal(t, "EGLL", item.Arrival.Ident)
	assert.Equal(t, 1200, item.TargetPayloadKg)
	assert.Equal(t, 800, item.TargetFuelKg)

	assert.Equal(t, "Bearer test-token", server.LastAuth())
}

func TestFlightTreatsNoContentAsNoFlight(t *testing.T) {
	server := apitest.NewServer(t)

	item, err := newClient(t, server).Flight(t.Context())
	require.NoError(t, err)
	assert.Nil(t, item, "no flight is not an error")
}

func TestSendPositionsCarriesTheBatchAndReadsTheResult(t *testing.T) {
	occurred := time.Date(2026, time.September, 11, 9, 12, 0, 0, time.UTC)

	server := apitest.NewServer(t)
	server.SetResult(api.PositionsResult{
		Status: "enroute",
		Events: []api.Event{{Kind: "takeoff_flaps_set", Params: map[string]string{"flaps": "0.3"}, OccurredAt: occurred}},
	})

	result, err := newClient(t, server).SendPositions(t.Context(), 42, []telemetry.Position{{UUID: "abc", FuelKg: 4200, OnGround: true}})
	require.NoError(t, err)

	assert.Equal(t, "enroute", result.Status)
	require.Len(t, result.Events, 1)
	assert.Equal(t, "takeoff_flaps_set", result.Events[0].Kind)
	assert.Equal(t, map[string]string{"flaps": "0.3"}, result.Events[0].Params)
	assert.Equal(t, occurred, result.Events[0].OccurredAt)

	assert.Equal(t, 42, server.LastFlightID(), "the batch names the flight it belongs to")

	sent := server.Sent()
	require.Len(t, sent, 1)
	assert.Equal(t, "abc", sent[0].UUID)
	assert.InDelta(t, 4200, sent[0].FuelKg, 0.001)
	assert.True(t, sent[0].OnGround)
}

func TestSendPositionsReportsAFinishedFlight(t *testing.T) {
	server := apitest.NewServer(t)
	server.Fail(func(w http.ResponseWriter) bool { return apitest.ValidationError(w, "flight_not_active") })

	_, err := newClient(t, server).SendPositions(t.Context(), 42, []telemetry.Position{{UUID: "abc"}})
	assert.True(t, api.FailedWith(err, "flight_not_active"))
	assert.False(t, api.SignedOut(err), "a rejected flight is not a rejected token")
}

func TestSendPositionsReportsARefusedToken(t *testing.T) {
	server := apitest.NewServer(t)
	server.Fail(apitest.Unauthorized)

	_, err := newClient(t, server).SendPositions(t.Context(), 42, []telemetry.Position{{UUID: "abc"}})
	assert.True(t, api.SignedOut(err))
}

func TestRevokeHandsTheTokenBack(t *testing.T) {
	server := apitest.NewServer(t)

	require.NoError(t, newClient(t, server).Revoke(t.Context()))
	assert.True(t, server.WasRevoked())
}

func TestRequestsFailOnAnExpiredContext(t *testing.T) {
	server := apitest.NewServer(t)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := newClient(t, server).Flight(ctx)
	assert.ErrorIs(t, err, context.Canceled)
}
