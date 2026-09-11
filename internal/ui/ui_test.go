package ui

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"github.com/ollie/atm-tracker/internal/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	log.SetOutput(io.Discard)
	os.Exit(m.Run())
}

func testUI(t *testing.T) *UI {
	t.Helper()

	return New(test.NewApp(), Actions{})
}

func TestSignedInSwapsTheButtons(t *testing.T) {
	u := testUI(t)

	u.SignedIn()
	assert.False(t, u.loginButton.Visible(), "log in stays offered after signing in")
	assert.True(t, u.logoutButton.Visible())
	assert.Equal(t, "looking for your flight", u.flightValue.Text)

	u.SignedOut()
	assert.True(t, u.loginButton.Visible())
	assert.False(t, u.logoutButton.Visible())
	assert.Equal(t, "log in to start tracking", u.flightValue.Text)
}

func TestSigningInDisablesTheButtonUntilTheBrowserAnswers(t *testing.T) {
	u := testUI(t)

	u.SigningIn()
	assert.True(t, u.loginButton.Disabled(), "a second click would open a second browser tab")
	assert.Equal(t, "waiting for the browser", u.flightValue.Text)

	u.SignedOut()
	assert.False(t, u.loginButton.Disabled(), "a failed login must be retryable")
}

func TestStartFlightShowsTheRouteAndTargets(t *testing.T) {
	u := testUI(t)

	u.StartFlight(&api.FlightInfo{
		Status:          "boarding",
		Departure:       api.AirportInfo{Ident: "LKPR"},
		Arrival:         api.AirportInfo{Ident: "EGLL"},
		TargetPayloadKg: 1200,
		TargetFuelKg:    800,
	})

	assert.Equal(t, "LKPR → EGLL", u.flightValue.Text)
	assert.Equal(t, "boarding", u.statusValue.Text)
	assert.Equal(t, "1200 kg payload, 800 kg fuel", u.targetsValue.Text)
	assert.Equal(t, "—", u.eventValue.Text)
}

func TestSetNoFlightClearsTheFlightFields(t *testing.T) {
	u := testUI(t)
	u.StartFlight(&api.FlightInfo{Status: "taxi_to_gate", Departure: api.AirportInfo{Ident: "LKPR"}, Arrival: api.AirportInfo{Ident: "EGLL"}})

	u.SetNoFlight()

	assert.Equal(t, "no flight to fly", u.flightValue.Text)
	assert.Equal(t, "—", u.statusValue.Text)
	assert.Equal(t, "—", u.targetsValue.Text)
	assert.Equal(t, "not tracking", u.simValue.Text)
}

func TestSetLiveTellsThePlayerWhetherTheSimIsTalking(t *testing.T) {
	u := testUI(t)

	u.SetLive(true)
	assert.Equal(t, "receiving", u.simValue.Text)

	u.SetLive(false)
	assert.Equal(t, "waiting for X-Plane", u.simValue.Text)
}

func TestSetProgressShowsTheLastEventInZulu(t *testing.T) {
	u := testUI(t)
	at := time.Date(2026, time.September, 11, 9, 12, 0, 0, time.UTC)

	u.SetProgress(&api.PositionsResult{
		Status: "enroute",
		Events: []api.Event{
			{Kind: "taxi", OccurredAt: at},
			{Kind: "takeoff_flaps_set", OccurredAt: at.Add(time.Minute)},
		},
	})

	assert.Equal(t, "enroute", u.statusValue.Text)
	assert.Equal(t, "takeoff flaps set at 09:13:00Z", u.eventValue.Text, "the newest event wins")
}

func TestSetProgressKeepsTheLastEventWhenABatchHasNone(t *testing.T) {
	u := testUI(t)
	u.SetProgress(&api.PositionsResult{
		Status: "taxi",
		Events: []api.Event{{Kind: "taxi", OccurredAt: time.Date(2026, time.September, 11, 9, 12, 0, 0, time.UTC)}},
	})

	u.SetProgress(&api.PositionsResult{Status: "enroute"})

	assert.Equal(t, "enroute", u.statusValue.Text)
	assert.Equal(t, "taxi at 09:12:00Z", u.eventValue.Text, "a quiet batch must not blank the timeline")
}

func TestWarningsOutliveTheBatchThatRaisedThem(t *testing.T) {
	u := testUI(t)
	u.StartFlight(&api.FlightInfo{Departure: api.AirportInfo{Ident: "LKPR"}, Arrival: api.AirportInfo{Ident: "EGLL"}})

	u.SetProgress(&api.PositionsResult{
		Status: "cancelled",
		Events: []api.Event{{Kind: "fuel_anomaly", OccurredAt: time.Now()}},
	})
	require.Equal(t, warningKinds["fuel_anomaly"], u.noteValue.Text)

	u.SetProgress(&api.PositionsResult{Status: "cancelled"})
	assert.Equal(t, warningKinds["fuel_anomaly"], u.noteValue.Text, "the rejection is the whole story, it must not scroll away")

	u.SetNoFlight()
	assert.Equal(t, warningKinds["fuel_anomaly"], u.noteValue.Text, "the flight ending is when the player reads it")

	u.StartFlight(&api.FlightInfo{Departure: api.AirportInfo{Ident: "EGLL"}, Arrival: api.AirportInfo{Ident: "LKPR"}})
	assert.Empty(t, u.noteValue.Text, "the next flight starts clean")
}

func TestANoteIsClearedByTheNextSuccess(t *testing.T) {
	u := testUI(t)

	u.SetNote("cannot reach the game, holding 12 positions")
	require.Equal(t, "cannot reach the game, holding 12 positions", u.noteValue.Text)

	u.SetProgress(&api.PositionsResult{Status: "enroute"})
	assert.Empty(t, u.noteValue.Text, "the batch got through, the note is stale")
}

func TestAWarningHidesTheNoteUnderIt(t *testing.T) {
	u := testUI(t)

	u.SetProgress(&api.PositionsResult{
		Status: "enroute",
		Events: []api.Event{{Kind: "not_at_departure", OccurredAt: time.Now()}},
	})
	u.SetNote("cannot reach the game, holding 12 positions")

	assert.Equal(t, warningKinds["not_at_departure"], u.noteValue.Text, "a warning outranks a passing note")
}

func TestLogWriterKeepsTheTailOfTheLog(t *testing.T) {
	u := testUI(t)
	w := u.LogWriter()

	seeded := make([]string, logLines)
	for i := range seeded {
		seeded[i] = fmt.Sprintf("line %d", i)
	}
	u.logText.SetText(strings.Join(seeded, "\n"))

	n, err := w.Write([]byte("flying\n"))
	require.NoError(t, err)
	assert.Equal(t, len("flying\n"), n, "the logger must see every byte written")

	lines := strings.Split(u.logText.Text, "\n")
	assert.Len(t, lines, logLines, "a long flight must not grow the log pane without bound")
	assert.Equal(t, "line 1", lines[0], "the oldest line is the one dropped")
	assert.Equal(t, "flying", lines[len(lines)-1])
}
