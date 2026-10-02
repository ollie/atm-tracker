package xplane

import (
	"context"
	"encoding/binary"
	"io"
	"log"
	"net"
	"os"
	"testing"
	"time"

	"github.com/ollie/atm-tracker/internal/telemetry"
	"github.com/ollie/atm-tracker/internal/xplane/xplanetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const waitTime = 2 * time.Second

func TestMain(m *testing.M) {
	log.SetOutput(io.Discard)
	os.Exit(m.Run())
}

func TestMakeRREF(t *testing.T) {
	packet := makeRREF(2, 7, "sim/time/paused")

	require.Len(t, packet, requestBytes, "an RREF request is 413 bytes")
	assert.Equal(t, "RREF\x00", string(packet[0:5]))
	assert.Equal(t, uint32(2), binary.LittleEndian.Uint32(packet[5:9]), "frequency")
	assert.Equal(t, uint32(7), binary.LittleEndian.Uint32(packet[9:13]), "index")
	assert.Equal(t, "sim/time/paused", xplanetest.CString(packet[13:]))
	assert.Equal(t, make([]byte, 400-len("sim/time/paused")), packet[13+len("sim/time/paused"):], "the name is zero padded")
}

func TestEveryEngineHasItsOwnDataref(t *testing.T) {
	var p telemetry.Position

	for i := range telemetry.MaxEngines {
		item, ok := datarefsMap[engineDatarefAt+int32(i)]
		require.Truef(t, ok, "no dataref for engine %d", i)

		item.assign(&p, 1)
	}

	assert.Equal(t, telemetry.MaxEngines, p.EnginesRunning, "the engine datarefs do not each light their own engine")
}

func TestSubscribeAsksForEveryDataref(t *testing.T) {
	sim := newSim(t)
	conn, addr := trackerSocket(t, sim)

	require.NoError(t, Subscribe(conn, addr))

	got := sim.Subscriptions()
	require.Len(t, got, DatarefCount())

	for index, dataref := range datarefsMap {
		assert.Equal(t, xplanetest.Subscription{Freq: subscribeHz, Name: dataref.name}, got[index])
	}
}

func TestUnsubscribeAsksForZeroHz(t *testing.T) {
	sim := newSim(t)
	conn, addr := trackerSocket(t, sim)

	require.NoError(t, Subscribe(conn, addr))
	sim.Subscriptions()

	Unsubscribe(conn, addr)

	for index, dataref := range sim.Subscriptions() {
		assert.Zerof(t, dataref.Freq, "index %d still subscribed", index)
	}
}

func TestReaderDecodesWhatTheSimSends(t *testing.T) {
	sim, updates, _ := startReader(t)

	sim.Send(
		xplanetest.Update{Index: 15, Value: 4200.5},
		xplanetest.Update{Index: 16, Value: 1},
	)

	assert.Equal(t, Update{Index: 15, Value: 4200.5}, receive(t, updates))
	assert.Equal(t, Update{Index: 16, Value: 1}, receive(t, updates))
}

func TestReaderIgnoresPacketsThatAreNotRREF(t *testing.T) {
	sim, updates, ctx := startReader(t)

	sim.SendRaw("BECN\x00hello")
	sim.Send(xplanetest.Update{Index: 13, Value: 0.78})

	assert.Equal(t, Update{Index: 13, Value: 0.78}, receive(t, updates), "the good packet still arrived")
	assert.NoError(t, ctx.Err(), "a junk packet must not end the flight")
}

func TestReaderEndsTheFlightWhenTheSocketCloses(t *testing.T) {
	sim := newSim(t)
	conn, _ := trackerSocket(t, sim)

	updates := make(chan Update, UpdateBuffer)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	stopped := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)
		RunReader(ctx, conn, updates, func() { close(stopped); cancel() })
	}()

	require.NoError(t, conn.Close())

	waitForClose(t, stopped, "the reader did not tear the flight down")
	waitForClose(t, done, "the reader did not return")
}

func startReader(t *testing.T) (*xplanetest.Sim, <-chan Update, context.Context) {
	t.Helper()

	sim := newSim(t)
	conn, addr := trackerSocket(t, sim)

	require.NoError(t, Subscribe(conn, addr))
	sim.Subscriptions()

	updates := make(chan Update, UpdateBuffer)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})

	go func() { defer close(done); RunReader(ctx, conn, updates, cancel) }()
	t.Cleanup(func() { cancel(); _ = conn.Close(); <-done })

	return sim, updates, ctx
}

func newSim(t *testing.T) *xplanetest.Sim {
	t.Helper()

	return xplanetest.NewSim(t, DatarefCount())
}

func trackerSocket(t *testing.T, sim *xplanetest.Sim) (*net.UDPConn, *net.UDPAddr) {
	t.Helper()

	addr, err := net.ResolveUDPAddr("udp", sim.Addr)
	require.NoError(t, err)

	conn, err := net.ListenUDP("udp", nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	return conn, addr
}

func receive(t *testing.T, updates <-chan Update) Update {
	t.Helper()

	select {
	case update := <-updates:
		return update
	case <-time.After(waitTime):
		t.Fatal("no dataref update arrived")
		return Update{}
	}
}

func waitForClose(t *testing.T, done <-chan struct{}, message string) {
	t.Helper()

	select {
	case <-done:
	case <-time.After(waitTime):
		t.Fatal(message)
	}
}

func TestPauseStopsAtPauseOnWhenTheSimObeys(t *testing.T) {
	sim := newSim(t)
	result := startPause(sim, waitTime)

	assert.Equal(t, makeRREF(pausedHz, pausedIndex, pausedDataref), sim.Packet(), "the pause state is read on this socket")
	assert.Equal(t, "CMND\x00sim/operation/pause_on", string(sim.Packet()))

	sim.Send(xplanetest.Update{Index: pausedIndex, Value: 0})
	sim.Send(xplanetest.Update{Index: pausedIndex, Value: 1})

	require.NoError(t, pauseResult(t, result))
	assert.Equal(t, makeRREF(0, pausedIndex, pausedDataref), sim.Packet(), "no toggle went out, the stale 0 must not count")
}

func TestPauseTogglesWhenTheSimIgnoresPauseOn(t *testing.T) {
	sim := newSim(t)
	result := startPause(sim, waitTime)

	sim.Packet()
	assert.Equal(t, "CMND\x00sim/operation/pause_on", string(sim.Packet()))
	for range int(waitTime / (time.Second / pausedHz)) {
		sim.Send(xplanetest.Update{Index: pausedIndex, Value: 0})
	}

	assert.Equal(t, "CMND\x00sim/operation/pause_toggle", string(sim.Packet()), "XP11 only knows the toggle")
	assert.Equal(t, makeRREF(0, pausedIndex, pausedDataref), sim.Packet())
	require.NoError(t, pauseResult(t, result))
}

func TestPauseDoesNotToggleWhenTheSimGoesQuiet(t *testing.T) {
	sim := newSim(t)
	result := startPause(sim, 300*time.Millisecond)

	sim.Packet()
	sim.Send(xplanetest.Update{Index: pausedIndex, Value: 0})
	assert.Equal(t, "CMND\x00sim/operation/pause_on", string(sim.Packet()))

	require.ErrorContains(t, pauseResult(t, result), "quiet")
	assert.Equal(t, makeRREF(0, pausedIndex, pausedDataref), sim.Packet(), "a stalled XP12 has paused, toggling would unpause it")
}

func TestPauseFailsWhenXPlaneIsSilent(t *testing.T) {
	sim := newSim(t)
	result := startPause(sim, 50*time.Millisecond)

	sim.Packet()
	sim.Packet()

	require.ErrorContains(t, pauseResult(t, result), "answer")
	assert.Equal(t, makeRREF(0, pausedIndex, pausedDataref), sim.Packet(), "still unsubscribes")
}

func startPause(sim *xplanetest.Sim, settle time.Duration) <-chan error {
	result := make(chan error, 1)
	go func() { result <- Pause(sim.Addr, settle) }()

	return result
}

func pauseResult(t *testing.T, result <-chan error) error {
	t.Helper()

	select {
	case err := <-result:
		return err
	case <-time.After(waitTime):
		t.Fatal("Pause did not return")
		return nil
	}
}
