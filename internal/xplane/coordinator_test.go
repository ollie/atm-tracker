package xplane

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const settleTime = 50 * time.Millisecond

func testTimings() Timings {
	return Timings{Snapshot: 5 * time.Millisecond, Silence: time.Hour, Resubscribe: time.Hour}
}

func silentSimTimings() Timings {
	return Timings{Snapshot: 5 * time.Millisecond, Silence: 20 * time.Millisecond, Resubscribe: 5 * time.Millisecond}
}

type coordinatorHarness struct {
	t         *testing.T
	updates   chan Update
	snapshots chan Snapshot
	live      chan bool
	resubs    chan struct{}
}

func startCoordinator(t *testing.T, tm Timings) *coordinatorHarness {
	t.Helper()

	h := &coordinatorHarness{
		t:         t,
		updates:   make(chan Update, UpdateBuffer),
		snapshots: make(chan Snapshot, 16),
		live:      make(chan bool, 16),
		resubs:    make(chan struct{}, 16),
	}

	ctx, stop := context.WithCancel(t.Context())
	done := make(chan struct{})

	c := &Coordinator{
		Updates:     h.updates,
		Snapshots:   h.snapshots,
		Resubscribe: func() { h.resubs <- struct{}{} },
		SetLive:     func(live bool) { h.live <- live },
		Timings:     tm,
	}

	go func() { defer close(done); c.Run(ctx) }()
	t.Cleanup(func() { stop(); <-done })

	return h
}

func (h *coordinatorHarness) pause(paused bool) {
	value := float32(0)
	if paused {
		value = 1
	}

	h.updates <- Update{Index: 1, Value: value}
}

func (h *coordinatorHarness) next() Snapshot {
	h.t.Helper()

	select {
	case s := <-h.snapshots:
		return s
	case <-time.After(waitTime):
		h.t.Fatal("no snapshot arrived")
		return Snapshot{}
	}
}

func (h *coordinatorHarness) nextPaused(paused bool) Snapshot {
	h.t.Helper()

	for {
		s := h.next()
		if s.Data.Paused == paused {
			return s
		}

		require.Falsef(h.t, s.Flush, "a snapshot before the pause change asked for a flush: %+v", s.Data)
	}
}

func (h *coordinatorHarness) expectSilence() {
	h.t.Helper()

	select {
	case s := <-h.snapshots:
		h.t.Fatalf("a snapshot arrived while paused: %+v", s.Data)
	case <-time.After(settleTime):
	}
}

func TestCoordinatorIsQuietUntilTheSimTalks(t *testing.T) {
	h := startCoordinator(t, testTimings())

	assert.False(t, <-h.live, "the sim starts as not receiving")
	h.expectSilence()
}

func TestCoordinatorSnapshotsAreStampedForTheServer(t *testing.T) {
	h := startCoordinator(t, testTimings())
	h.updates <- Update{Index: 15, Value: 4200}

	first := h.next()
	assert.False(t, first.Flush, "a plain snapshot does not force a send")
	assert.NotEmpty(t, first.Data.UUID, "the server deduplicates on uuid")
	assert.Equal(t, time.UTC, first.Data.ReportedAt.Location(), "the server orders by reportedAt")
	assert.InDelta(t, 4200, first.Data.FuelKg, 0.001)

	assert.NotEqual(t, first.Data.UUID, h.next().Data.UUID, "two snapshots share a uuid")
}

func TestCoordinatorFlushesOnPauseAndStaysQuiet(t *testing.T) {
	h := startCoordinator(t, testTimings())
	h.updates <- Update{Index: 15, Value: 4200}
	h.next()

	h.pause(true)
	assert.True(t, h.nextPaused(true).Flush, "entering pause must flush what is held")

	h.expectSilence()

	h.pause(false)
	assert.True(t, h.nextPaused(false).Flush, "leaving pause must flush immediately")
	assert.False(t, h.next().Flush, "the flight goes back to plain snapshots")
}

func TestCoordinatorResubscribesWhenTheSimGoesQuiet(t *testing.T) {
	h := startCoordinator(t, silentSimTimings())

	assert.False(t, <-h.live)

	h.updates <- Update{Index: 15, Value: 4200}
	assert.True(t, <-h.live, "packets flowing means receiving")

	select {
	case <-h.resubs:
	case <-time.After(waitTime):
		t.Fatal("the tracker did not re-subscribe after the silence")
	}

	assert.False(t, <-h.live, "silence means waiting for X-Plane again")
}
