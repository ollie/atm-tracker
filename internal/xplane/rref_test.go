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

	"github.com/ollie/atm-tracker/internal/sim"
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

func TestConversions(t *testing.T) {
	assert.Equal(t, 3281, metersToFeet(1000))
	assert.Equal(t, telemetry.Kts(194), mpsToKts(100))
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
	fakeSim := newSim(t)
	conn, addr := trackerSocket(t, fakeSim)

	require.NoError(t, subscribe(conn, addr))

	got := fakeSim.Subscriptions()
	require.Len(t, got, DatarefCount())

	for index, dataref := range datarefsMap {
		assert.Equal(t, xplanetest.Subscription{Freq: subscribeHz, Name: dataref.name}, got[index])
	}
}

func TestUnsubscribeAsksForZeroHz(t *testing.T) {
	fakeSim := newSim(t)
	conn, addr := trackerSocket(t, fakeSim)

	require.NoError(t, subscribe(conn, addr))
	fakeSim.Subscriptions()

	unsubscribe(conn, addr)

	for index, dataref := range fakeSim.Subscriptions() {
		assert.Zerof(t, dataref.Freq, "index %d still subscribed", index)
	}
}

func TestReaderDecodesWhatTheSimSends(t *testing.T) {
	fakeSim, updates, _ := startReader(t)

	fakeSim.Send(
		xplanetest.Update{Index: 15, Value: 4200.5},
		xplanetest.Update{Index: 16, Value: 1},
	)

	var p telemetry.Position
	receive(t, updates)(&p)
	receive(t, updates)(&p)

	assert.InDelta(t, 4200.5, p.FuelKg, 0.001)
	assert.True(t, p.OnGround)
}

func TestReaderSkipsDatarefsItNeverAskedFor(t *testing.T) {
	fakeSim, updates, ctx := startReader(t)

	fakeSim.Send(
		xplanetest.Update{Index: 999, Value: 1},
		xplanetest.Update{Index: 13, Value: 0.78},
	)

	var p telemetry.Position
	receive(t, updates)(&p)

	assert.InDelta(t, 0.78, p.Mach, 0.001, "the known dataref still arrived")
	assert.NoError(t, ctx.Err(), "a stray index must not end the flight")
}

func TestReaderIgnoresPacketsThatAreNotRREF(t *testing.T) {
	fakeSim, updates, ctx := startReader(t)

	fakeSim.SendRaw("BECN\x00hello")
	fakeSim.Send(xplanetest.Update{Index: 13, Value: 0.78})

	var p telemetry.Position
	receive(t, updates)(&p)

	assert.InDelta(t, 0.78, p.Mach, 0.001, "the good packet still arrived")
	assert.NoError(t, ctx.Err(), "a junk packet must not end the flight")
}

func TestReaderEndsTheFlightWhenTheSocketCloses(t *testing.T) {
	fakeSim := newSim(t)
	conn, _ := trackerSocket(t, fakeSim)

	updates := make(chan sim.Update, sim.UpdateBuffer)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	stopped := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)
		runReader(ctx, conn, updates, func() { close(stopped); cancel() })
	}()

	require.NoError(t, conn.Close())

	waitForClose(t, stopped, "the reader did not tear the flight down")
	waitForClose(t, done, "the reader did not return")
}

func startReader(t *testing.T) (*xplanetest.Sim, <-chan sim.Update, context.Context) {
	t.Helper()

	fakeSim := newSim(t)
	conn, addr := trackerSocket(t, fakeSim)

	require.NoError(t, subscribe(conn, addr))
	fakeSim.Subscriptions()

	updates := make(chan sim.Update, sim.UpdateBuffer)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})

	go func() { defer close(done); runReader(ctx, conn, updates, cancel) }()
	t.Cleanup(func() { cancel(); _ = conn.Close(); <-done })

	return fakeSim, updates, ctx
}

func newSim(t *testing.T) *xplanetest.Sim {
	t.Helper()

	return xplanetest.NewSim(t, DatarefCount())
}

func trackerSocket(t *testing.T, fakeSim *xplanetest.Sim) (*net.UDPConn, *net.UDPAddr) {
	t.Helper()

	addr, err := net.ResolveUDPAddr("udp", fakeSim.Addr)
	require.NoError(t, err)

	conn, err := net.ListenUDP("udp", nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	return conn, addr
}

func receive(t *testing.T, updates <-chan sim.Update) sim.Update {
	t.Helper()

	select {
	case update := <-updates:
		return update
	case <-time.After(waitTime):
		t.Fatal("no dataref update arrived")
		return nil
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
	fakeSim := newSim(t)
	result := startPause(fakeSim, waitTime)

	assert.Equal(t, makeRREF(pausedHz, pausedIndex, pausedDataref), fakeSim.Packet(), "the pause state is read on this socket")
	assert.Equal(t, "CMND\x00sim/operation/pause_on", string(fakeSim.Packet()))

	fakeSim.Send(xplanetest.Update{Index: pausedIndex, Value: 0})
	fakeSim.Send(xplanetest.Update{Index: pausedIndex, Value: 1})

	require.NoError(t, pauseResult(t, result))
	assert.Equal(t, makeRREF(0, pausedIndex, pausedDataref), fakeSim.Packet(), "no toggle went out, the stale 0 must not count")
}

func TestPauseTogglesWhenTheSimIgnoresPauseOn(t *testing.T) {
	fakeSim := newSim(t)
	result := startPause(fakeSim, waitTime)

	fakeSim.Packet()
	assert.Equal(t, "CMND\x00sim/operation/pause_on", string(fakeSim.Packet()))
	for range int(waitTime / (time.Second / pausedHz)) {
		fakeSim.Send(xplanetest.Update{Index: pausedIndex, Value: 0})
	}

	assert.Equal(t, "CMND\x00sim/operation/pause_toggle", string(fakeSim.Packet()), "XP11 only knows the toggle")
	assert.Equal(t, makeRREF(0, pausedIndex, pausedDataref), fakeSim.Packet())
	require.NoError(t, pauseResult(t, result))
}

func TestPauseDoesNotToggleWhenTheSimGoesQuiet(t *testing.T) {
	fakeSim := newSim(t)
	result := startPause(fakeSim, 300*time.Millisecond)

	fakeSim.Packet()
	fakeSim.Send(xplanetest.Update{Index: pausedIndex, Value: 0})
	assert.Equal(t, "CMND\x00sim/operation/pause_on", string(fakeSim.Packet()))

	require.ErrorContains(t, pauseResult(t, result), "quiet")
	assert.Equal(t, makeRREF(0, pausedIndex, pausedDataref), fakeSim.Packet(), "a stalled XP12 has paused, toggling would unpause it")
}

func TestPauseFailsWhenXPlaneIsSilent(t *testing.T) {
	fakeSim := newSim(t)
	result := startPause(fakeSim, 50*time.Millisecond)

	fakeSim.Packet()
	fakeSim.Packet()

	require.ErrorContains(t, pauseResult(t, result), "answer")
	assert.Equal(t, makeRREF(0, pausedIndex, pausedDataref), fakeSim.Packet(), "still unsubscribes")
}

func startPause(fakeSim *xplanetest.Sim, settle time.Duration) <-chan error {
	result := make(chan error, 1)
	go func() { result <- pause(fakeSim.Addr, settle) }()

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
