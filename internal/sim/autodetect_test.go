package sim

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ollie/atm-tracker/internal/telemetry"
)

type fakeSim struct {
	name     string
	openErr  error
	pauseErr error
	opened   []*fakeConnection
	paused   int
}

type fakeConnection struct {
	updates      chan Update
	closed       bool
	resubscribed int
}

func (s *fakeSim) Name() string { return s.name }

func (s *fakeSim) Open() (Connection, error) {
	if s.openErr != nil {
		return nil, s.openErr
	}
	conn := &fakeConnection{updates: make(chan Update, 1)}
	s.opened = append(s.opened, conn)
	return conn, nil
}

func (s *fakeSim) Pause() error {
	s.paused++
	return s.pauseErr
}

func (c *fakeConnection) Resubscribe() error { c.resubscribed++; return nil }
func (c *fakeConnection) Close()             { c.closed = true }

func (c *fakeConnection) Run(ctx context.Context, updates chan<- Update, _ context.CancelFunc) {
	for {
		select {
		case u := <-c.updates:
			updates <- u
		case <-ctx.Done():
			return
		}
	}
}

func twoSims() (*fakeSim, *fakeSim, *Autodetect) {
	first := &fakeSim{name: "first", openErr: errors.New("first is not running")}
	second := &fakeSim{name: "second"}
	return first, second, &Autodetect{Sims: []Sim{first, second}}
}

func hear(t *testing.T, a *Autodetect, from *fakeSim) Connection {
	t.Helper()

	conn, err := a.Open()
	require.NoError(t, err)

	updates := make(chan Update, 1)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	go conn.Run(ctx, updates, cancel)

	from.opened[len(from.opened)-1].updates <- func(*telemetry.Position) {}
	select {
	case <-updates:
	case <-time.After(2 * time.Second):
		t.Fatal("the update never came through")
	}

	return conn
}

func TestAutodetectOpensTheFirstSimThatAnswers(t *testing.T) {
	first, second, a := twoSims()

	_, err := a.Open()
	require.NoError(t, err)
	assert.Empty(t, first.opened)
	assert.Len(t, second.opened, 1)
}

func TestAutodetectPrefersTheFirstSimWhenItAnswers(t *testing.T) {
	first, second, a := twoSims()
	first.openErr = nil

	_, err := a.Open()
	require.NoError(t, err)
	assert.Len(t, first.opened, 1)
	assert.Empty(t, second.opened, "the second sim is not even tried")
}

func TestAutodetectReportsEveryErrorWhenNoSimAnswers(t *testing.T) {
	first, second, a := twoSims()
	second.openErr = errors.New("second is not running either")

	_, err := a.Open()
	require.EqualError(t, err, "first is not running\nsecond is not running either")

	first.pauseErr = errors.New("first is not running")
	second.pauseErr = errors.New("second did not answer")
	require.EqualError(t, a.Pause(), "first is not running\nsecond did not answer")
}

func TestAutodetectKeepsLookingUntilDataArrives(t *testing.T) {
	first, _, a := twoSims()

	_, err := a.Open()
	require.NoError(t, err)
	assert.Equal(t, "–", a.Name(), "an open sim that has not sent anything is not the sim yet")

	first.openErr = nil
	_, err = a.Open()
	require.NoError(t, err)
	assert.Len(t, first.opened, 1, "nothing was heard from the second sim, so the first gets its turn")
}

func TestAutodetectStaysWithTheSimItHeardFrom(t *testing.T) {
	first, second, a := twoSims()

	hear(t, a, second)
	assert.Equal(t, "second", a.Name())

	first.openErr = nil
	second.openErr = errors.New("second has quit")
	_, err := a.Open()
	require.EqualError(t, err, "second has quit", "the first sim appearing later does not count")
	assert.Empty(t, first.opened)
}

func TestAutodetectPausesTheSimItHeardFrom(t *testing.T) {
	first, second, a := twoSims()

	hear(t, a, second)
	require.NoError(t, a.Pause())
	assert.Equal(t, 0, first.paused)
	assert.Equal(t, 1, second.paused)

	second.pauseErr = errors.New("second has quit")
	require.EqualError(t, a.Pause(), "second has quit", "the flight's sim is the only one asked")
	assert.Equal(t, 0, first.paused)
}

func TestAutodetectPausesWhoeverAnswersBeforeAnyFlight(t *testing.T) {
	first, second, a := twoSims()
	first.pauseErr = errors.New("first is not running")

	require.NoError(t, a.Pause())
	assert.Equal(t, 1, first.paused)
	assert.Equal(t, 1, second.paused)
	assert.Equal(t, "second", a.Name(), "a pause that answers is as good as data")

	first.openErr = nil
	_, err := a.Open()
	require.NoError(t, err)
	assert.Empty(t, first.opened)
	assert.Len(t, second.opened, 1)
}

func TestResubscribeEndsTheFlightWhenAPreferredSimAppears(t *testing.T) {
	first, second, a := twoSims()

	conn, err := a.Open()
	require.NoError(t, err)

	require.NoError(t, conn.Resubscribe())
	assert.Equal(t, 1, second.opened[0].resubscribed, "nothing better around, the sim is asked again")
	assert.False(t, second.opened[0].closed)

	first.openErr = nil
	require.NoError(t, conn.Resubscribe())
	assert.True(t, second.opened[0].closed, "closing the socket ends the flight, the next one opens the preferred sim")
	require.Len(t, first.opened, 1)
	assert.True(t, first.opened[0].closed, "the probe is not kept")
}

func TestResubscribeStopsLookingOnceASimWasHeard(t *testing.T) {
	first, second, a := twoSims()

	conn := hear(t, a, second)
	first.openErr = nil

	require.NoError(t, conn.Resubscribe())
	assert.Empty(t, first.opened)
	assert.False(t, second.opened[0].closed)
	assert.Equal(t, 1, second.opened[0].resubscribed)
}
