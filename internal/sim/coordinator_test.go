package sim

import (
	"context"
	"testing"
	"time"

	"github.com/ollie/atm-tracker/internal/telemetry"
	"github.com/stretchr/testify/assert"
)

const (
	settleTime = 50 * time.Millisecond
	waitTime   = 2 * time.Second
)

func testTimings() Timings {
	return Timings{Snapshot: 5 * time.Millisecond, Silence: time.Hour, Resubscribe: time.Hour}
}

func silentSimTimings() Timings {
	return Timings{Snapshot: 5 * time.Millisecond, Silence: 20 * time.Millisecond, Resubscribe: 5 * time.Millisecond}
}

type coordinatorHarness struct {
	t         *testing.T
	updates   chan Update
	snapshots chan telemetry.Position
	live      chan bool
	resubs    chan struct{}
}

func startCoordinator(t *testing.T, tm Timings) *coordinatorHarness {
	t.Helper()

	h := &coordinatorHarness{
		t:         t,
		updates:   make(chan Update, UpdateBuffer),
		snapshots: make(chan telemetry.Position, 16),
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
	h.updates <- func(p *telemetry.Position) { p.Paused = paused }
}

func (h *coordinatorHarness) fuel(kg float32) {
	h.updates <- func(p *telemetry.Position) { p.FuelKg = kg }
}

func (h *coordinatorHarness) next() telemetry.Position {
	h.t.Helper()

	select {
	case p := <-h.snapshots:
		return p
	case <-time.After(waitTime):
		h.t.Fatal("no snapshot arrived")
		return telemetry.Position{}
	}
}

func (h *coordinatorHarness) nextWhere(failure string, matches func(telemetry.Position) bool) {
	h.t.Helper()

	for deadline := time.Now().Add(waitTime); time.Now().Before(deadline); {
		if matches(h.next()) {
			return
		}
	}

	h.t.Fatal(failure)
}

func (h *coordinatorHarness) expectSilence() {
	h.t.Helper()

	select {
	case p := <-h.snapshots:
		h.t.Fatalf("a snapshot arrived before the sim talked: %+v", p)
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
	h.fuel(4200)

	first := h.next()
	assert.NotEmpty(t, first.UUID, "the server deduplicates on uuid")
	assert.Equal(t, time.UTC, first.ReportedAt.Location(), "the server orders by reportedAt")
	assert.InDelta(t, 4200, first.FuelKg, 0.001)

	assert.NotEqual(t, first.UUID, h.next().UUID, "two snapshots share a uuid")
}

func TestCoordinatorKeepsReadingAPausedSim(t *testing.T) {
	h := startCoordinator(t, testTimings())
	h.pause(true)
	h.fuel(4200)

	h.nextWhere("the paused sim went quiet", func(p telemetry.Position) bool { return p.Paused })

	h.fuel(4300)
	h.nextWhere("fuel loaded into a paused sim never reached the tracker", func(p telemetry.Position) bool {
		return p.Paused && p.FuelKg == 4300
	})
}

func TestCoordinatorReportsACrashOnce(t *testing.T) {
	h := startCoordinator(t, testTimings())
	h.fuel(4200)
	h.next()

	h.updates <- func(p *telemetry.Position) { p.Crashed = true }

	h.nextWhere("the crash never reached the tracker", func(p telemetry.Position) bool { return p.Crashed })

	assert.False(t, h.next().Crashed, "a crash the server ignored must not carry into later snapshots")
}

func TestCoordinatorResubscribesWhenTheSimGoesQuiet(t *testing.T) {
	h := startCoordinator(t, silentSimTimings())

	assert.False(t, <-h.live)

	h.fuel(4200)
	assert.True(t, <-h.live, "packets flowing means receiving")

	select {
	case <-h.resubs:
	case <-time.After(waitTime):
		t.Fatal("the tracker did not re-subscribe after the silence")
	}

	assert.False(t, <-h.live, "silence means waiting for the sim again")
}
