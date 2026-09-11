package tracker

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/ollie/atm-tracker/internal/api"
	"github.com/ollie/atm-tracker/internal/api/apitest"
	"github.com/ollie/atm-tracker/internal/telemetry"
	"github.com/ollie/atm-tracker/internal/xplane"
	"github.com/ollie/atm-tracker/internal/xplane/xplanetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testTimings() Timings {
	return Timings{Send: 20 * time.Millisecond, FlightPoll: 20 * time.Millisecond}
}

func testSimTimings() xplane.Timings {
	return xplane.Timings{Snapshot: 5 * time.Millisecond, Silence: time.Hour, Resubscribe: time.Hour}
}

type flightHarness struct {
	t       *testing.T
	sim     *xplanetest.Sim
	server  *apitest.Server
	display *spyDisplay
	session *session

	stop context.CancelFunc
	done chan struct{}
}

func startFlight(t *testing.T, item *api.FlightInfo) *flightHarness {
	t.Helper()

	h := &flightHarness{
		t:       t,
		sim:     xplanetest.NewSim(t, xplane.DatarefCount()),
		server:  apitest.NewServer(t),
		display: &spyDisplay{},
		done:    make(chan struct{}),
	}

	client := api.New(h.server.URL, "ATM Tracker test", apitest.StaticToken())

	h.session = &session{
		api:         client,
		ui:          h.display,
		onSignedOut: func() {},
		simAddr:     h.sim.Addr,
		timings:     testTimings(),
		simTimings:  testSimTimings(),
	}

	ctx, stop := context.WithCancel(t.Context())
	h.stop = stop

	go func() {
		defer close(h.done)
		defer client.Close()
		h.session.fly(ctx, item)
	}()

	h.sim.Subscriptions()

	return h
}

func (h *flightHarness) settle() {
	h.t.Helper()

	h.stop()

	select {
	case <-h.done:
	case <-time.After(waitTime):
		h.t.Fatal("the flight did not stop")
	}
}

func (h *flightHarness) waitForEnd(message string) {
	h.t.Helper()

	select {
	case <-h.done:
	case <-time.After(waitTime):
		h.t.Fatal(message)
	}

	h.stop()
}

func testFlight() *api.FlightInfo {
	return &api.FlightInfo{ID: 42, Departure: api.AirportInfo{Ident: "LKPR"}, Arrival: api.AirportInfo{Ident: "EGLL"}}
}

func TestAFlightCarriesSimTelemetryToTheGame(t *testing.T) {
	h := startFlight(t, testFlight())

	h.sim.Send(
		xplanetest.Update{Index: 15, Value: 4200},
		xplanetest.Update{Index: 16, Value: 1},
		xplanetest.Update{Index: 9, Value: 100},
	)

	h.server.WaitForPosts(1)
	h.settle()

	assert.Equal(t, 42, h.server.LastFlightID())

	sent := h.server.Sent()
	require.NotEmpty(t, sent, "the game heard nothing from the sim")

	first := sent[0]
	assert.NotEmpty(t, first.UUID, "the server deduplicates retries on uuid")
	assert.Equal(t, time.UTC, first.ReportedAt.Location(), "the log renders Zulu and the server orders by reportedAt")
	assert.InDelta(t, 4200, first.FuelKg, 0.001)
	assert.True(t, first.OnGround)
	assert.Equal(t, telemetry.Kts(194), first.GroundspeedKt, "100 m/s is 194 kt")
}

func TestAFlightShowsWhatTheGameSaysBack(t *testing.T) {
	h := startFlight(t, testFlight())
	occurred := time.Date(2026, time.September, 11, 9, 12, 0, 0, time.UTC)
	h.server.SetResult(api.PositionsResult{
		Status: "enroute",
		Events: []api.Event{{Kind: "takeoff_flaps_set", OccurredAt: occurred}},
	})

	h.sim.Send(xplanetest.Update{Index: 15, Value: 4200})

	h.server.WaitForPosts(2)
	h.settle()

	progress := h.display.Progress()
	require.NotNil(t, progress, "the player was never told how the flight is going")
	assert.Equal(t, "enroute", progress.Status)
	require.Len(t, progress.Events, 1)
	assert.Equal(t, "takeoff_flaps_set", progress.Events[0].Kind)
	assert.Equal(t, occurred, progress.Events[0].OccurredAt)

	assert.True(t, h.display.Live(), "packets are flowing, the sim pane must say so")
}

func TestAFlightStopsWhenTheGameSaysItIsOver(t *testing.T) {
	h := startFlight(t, testFlight())
	h.server.SetResult(api.PositionsResult{Status: "completed"})

	h.sim.Send(xplanetest.Update{Index: 15, Value: 4200})

	h.waitForEnd("the tracker kept flying a completed flight")

	require.NotNil(t, h.display.Progress())
	assert.Equal(t, "completed", h.display.Progress().Status)
}

func TestAFlightStopsWhenTheGameRejectsTheBatch(t *testing.T) {
	h := startFlight(t, testFlight())
	h.server.Fail(func(w http.ResponseWriter) bool { return apitest.ValidationError(w, "flight_not_active") })

	h.sim.Send(xplanetest.Update{Index: 15, Value: 4200})

	h.waitForEnd("the tracker kept sending to a flight the game had closed")
}

func TestAFlightHoldsPositionsTheGameWouldNotTake(t *testing.T) {
	h := startFlight(t, testFlight())
	h.server.Fail(func(w http.ResponseWriter) bool {
		w.WriteHeader(http.StatusInternalServerError)
		return true
	})

	h.sim.Send(xplanetest.Update{Index: 15, Value: 4200})

	h.server.WaitForPosts(2)

	sizes := h.server.BatchSizes()
	h.settle()

	require.GreaterOrEqual(t, len(sizes), 2)
	assert.Positive(t, sizes[0])
	assert.Greater(t, sizes[1], sizes[0], "a failed send must keep its positions for the retry")
	assert.Contains(t, h.display.Note(), "cannot reach the game")
}

func TestAFlightUnsubscribesWhenItEnds(t *testing.T) {
	h := startFlight(t, testFlight())

	h.settle()

	for index, dataref := range h.sim.Subscriptions() {
		assert.Zerof(t, dataref.Freq, "index %d is still subscribed, X-Plane would keep sending", index)
	}
}
