package tracker

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/ollie/atm-tracker/internal/api"
	"github.com/ollie/atm-tracker/internal/api/apitest"
	"github.com/ollie/atm-tracker/internal/sim"
	"github.com/ollie/atm-tracker/internal/telemetry"
	"github.com/ollie/atm-tracker/internal/xplane"
	"github.com/ollie/atm-tracker/internal/xplane/xplanetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testTimings() Timings {
	return Timings{Send: 20 * time.Millisecond, FlightPoll: 20 * time.Millisecond}
}

func testSimTimings() sim.Timings {
	return sim.Timings{Snapshot: 5 * time.Millisecond, Silence: time.Hour, Resubscribe: time.Hour}
}

type flightHarness struct {
	t       *testing.T
	sim     *xplanetest.Sim
	server  *apitest.Server
	display *spyDisplay
	session *session

	deboards chan struct{}
	stop     context.CancelFunc
	done     chan struct{}
}

func startFlight(t *testing.T, item *api.FlightInfo) *flightHarness {
	t.Helper()

	return startFlightWith(t, item, testTimings())
}

func startFlightWith(t *testing.T, item *api.FlightInfo, timings Timings) *flightHarness {
	t.Helper()

	h := &flightHarness{
		t:        t,
		sim:      xplanetest.NewSim(t, xplane.DatarefCount()),
		server:   apitest.NewServer(t),
		display:  &spyDisplay{},
		deboards: make(chan struct{}, 1),
		done:     make(chan struct{}),
	}

	client := api.New(h.server.URL, "ATM Tracker test", apitest.StaticToken())

	h.session = &session{
		api:         client,
		ui:          h.display,
		onSignedOut: func() {},
		sim:         &xplane.Sim{Addr: h.sim.Addr},
		timings:     timings,
		simTimings:  testSimTimings(),
		deboards:    h.deboards,
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

func (h *flightHarness) pressDeboard() {
	h.t.Helper()

	require.Eventually(h.t, func() bool { return h.display.Actual().fuelKg > 0 }, waitTime, time.Millisecond, "the sim never reached the tracker")
	h.deboards <- struct{}{}
}

func atTheGate() Timings {
	return Timings{Send: time.Hour, FlightPoll: 20 * time.Millisecond}
}

func testFlight() *api.FlightInfo {
	return &api.FlightInfo{ID: 42, Departure: api.AirportInfo{Code: "LKPR"}, Arrival: api.AirportInfo{Code: "EGLL"}}
}

func TestAFlightCarriesSimTelemetryToTheAPI(t *testing.T) {
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
	require.NotEmpty(t, sent, "the API heard nothing from the sim")

	first := sent[0]
	assert.NotEmpty(t, first.UUID, "the server deduplicates retries on uuid")
	assert.Equal(t, time.UTC, first.ReportedAt.Location(), "the log renders Zulu and the server orders by reportedAt")
	assert.InDelta(t, 4200, first.FuelKg, 0.001)
	assert.True(t, first.OnGround)
	assert.Equal(t, telemetry.Kts(194), first.GroundspeedKt, "100 m/s is 194 kt")
}

func TestAFlightShowsWhatTheAircraftIsCarrying(t *testing.T) {
	h := startFlight(t, testFlight())

	h.sim.Send(
		xplanetest.Update{Index: 11, Value: 5000},
		xplanetest.Update{Index: 14, Value: 6930},
		xplanetest.Update{Index: 15, Value: 780},
	)

	h.server.WaitForPosts(1)
	h.settle()

	actual := h.display.Actual()
	assert.InDelta(t, 1150, actual.payloadKg, 0.001, "the player cannot see the load the sim reported")
	assert.InDelta(t, 780, actual.fuelKg, 0.001)
}

func TestAFlightShowsWhetherTheEnginesAreRunning(t *testing.T) {
	h := startFlight(t, testFlight())

	h.sim.Send(
		xplanetest.Update{Index: 15, Value: 780},
		xplanetest.Update{Index: 21, Value: 1},
		xplanetest.Update{Index: 22, Value: 1},
	)

	h.server.WaitForPosts(1)
	h.settle()

	assert.Equal(t, 2, h.display.Actual().enginesRunning, "the Engines row cannot say whether the player may still deboard")
}

func TestPressingDeboardSendsTheLastPositionsAndSettlesTheFlight(t *testing.T) {
	h := startFlightWith(t, testFlight(), atTheGate())
	occurred := time.Date(2026, time.October, 21, 14, 2, 0, 0, time.UTC)
	h.server.SetDeboardResult(api.PositionsResult{
		Status: "completed",
		Events: []api.Event{{Kind: "deboard", OccurredAt: occurred}},
	})

	h.sim.Send(xplanetest.Update{Index: 15, Value: 780})
	h.pressDeboard()

	h.waitForEnd("the tracker kept flying after the player deboarded")

	assert.Positive(t, h.server.PositionsAtDeboard(), "the API resolves the landing airport from the last stored position, it must have it before settling")
	assert.Equal(t, 42, h.server.LastFlightID())

	progress := h.display.Progress()
	require.NotNil(t, progress, "the player was never told the flight settled")
	assert.Equal(t, "completed", progress.Status)
	require.Len(t, progress.Events, 1)
	assert.Equal(t, "deboard", progress.Events[0].Kind)
}

func TestDeboardWhoseReplyWasLostEndsTheFlightOnTheNextLook(t *testing.T) {
	h := startFlightWith(t, testFlight(), atTheGate())
	h.server.FailDeboard(func(w http.ResponseWriter) bool { return apitest.ValidationError(w, "flight_not_deboarding") })

	h.sim.Send(xplanetest.Update{Index: 15, Value: 780})
	h.pressDeboard()

	h.waitForEnd("the API has no flight for the player, the tracker must stop flying the settled one")
}

func TestDeboardTheAPIRefusesTellsThePlayerWhy(t *testing.T) {
	h := startFlightWith(t, testFlight(), atTheGate())
	h.server.SetFlight(testFlight())
	h.server.FailDeboard(func(w http.ResponseWriter) bool { return apitest.ValidationError(w, "no_airport_at_landing_site") })

	h.sim.Send(xplanetest.Update{Index: 15, Value: 780})
	h.pressDeboard()

	assert.Eventually(t, func() bool { return h.display.Note() != "" }, waitTime, time.Millisecond, "the refusal never reached the player")
	assert.Contains(t, h.display.Note(), "No airport here")

	h.settle()
}

func TestDeboardDuringAnOutageTellsThePlayerToPressAgain(t *testing.T) {
	h := startFlightWith(t, testFlight(), atTheGate())
	h.server.Fail(func(w http.ResponseWriter) bool {
		w.WriteHeader(http.StatusInternalServerError)
		return true
	})

	h.sim.Send(xplanetest.Update{Index: 15, Value: 780})
	h.pressDeboard()

	assert.Eventually(t, func() bool { return h.display.Note() != "" }, waitTime, time.Millisecond, "the player was not told the press was dropped")
	assert.Contains(t, h.display.Note(), "press Deboard again")
	assert.Zero(t, h.server.PositionsAtDeboard(), "the deboard must not be sent while positions are still unsent")

	h.settle()
}

func TestAFlightShowsWhatTheAPISaysBack(t *testing.T) {
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

	simName, live := h.display.Live()
	assert.True(t, live, "packets are flowing, the sim pane must say so")
	assert.Equal(t, "X-Plane", simName, "the Sim row names the sim")
}

func TestAFlightStopsWhenTheAPISaysItIsOver(t *testing.T) {
	h := startFlight(t, testFlight())
	h.server.SetResult(api.PositionsResult{Status: "completed"})

	h.sim.Send(xplanetest.Update{Index: 15, Value: 4200})

	h.waitForEnd("the tracker kept flying a completed flight")

	require.NotNil(t, h.display.Progress())
	assert.Equal(t, "completed", h.display.Progress().Status)
}

func TestAFlightStopsWhenTheAPIRejectsTheBatch(t *testing.T) {
	h := startFlight(t, testFlight())
	h.server.Fail(func(w http.ResponseWriter) bool { return apitest.ValidationError(w, "flight_not_active") })

	h.sim.Send(xplanetest.Update{Index: 15, Value: 4200})

	h.waitForEnd("the tracker kept sending to a flight the API had closed")
}

func TestAFlightHoldsPositionsTheAPIWouldNotTake(t *testing.T) {
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
	assert.Contains(t, h.display.Note(), "cannot reach the Air Transport Magnate website")
}

func TestAFlightUnsubscribesWhenItEnds(t *testing.T) {
	h := startFlight(t, testFlight())

	h.settle()

	for index, dataref := range h.sim.Subscriptions() {
		assert.Zerof(t, dataref.Freq, "index %d is still subscribed, X-Plane would keep sending", index)
	}
}
