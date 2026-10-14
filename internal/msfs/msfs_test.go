package msfs

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"log"
	"net"
	"os"
	"testing"
	"time"

	"github.com/ollie/atm-tracker/internal/msfs/msfstest"
	"github.com/ollie/atm-tracker/internal/sim"
	"github.com/ollie/atm-tracker/internal/telemetry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	waitTime   = 2 * time.Second
	settleTime = 100 * time.Millisecond
)

func TestMain(m *testing.M) {
	log.SetOutput(io.Discard)
	os.Exit(m.Run())
}

func TestPacketHeaderIsSizeProtocolTypeAndSendID(t *testing.T) {
	b := openPacket("x").bytes(7)

	assert.Len(t, b, 16+256+4+4+16)
	assert.Equal(t, uint32(len(b)), binary.LittleEndian.Uint32(b[0:4]))
	assert.Equal(t, uint32(protocolKittyHawk), binary.LittleEndian.Uint32(b[4:8]))
	assert.Equal(t, uint32(0xf0000001), binary.LittleEndian.Uint32(b[8:12]))
	assert.Equal(t, uint32(7), binary.LittleEndian.Uint32(b[12:16]))
	assert.Equal(t, "x", msfstest.CString(b[16:272]))
}

func TestEveryEngineHasItsOwnSimVar(t *testing.T) {
	require.Len(t, simVars, engineCombustionAt+telemetry.MaxEngines)
	assert.Equal(t, "GENERAL ENG COMBUSTION:1", simVars[engineCombustionAt].name)
	assert.Equal(t, "GENERAL ENG COMBUSTION:8", simVars[engineCombustionAt+telemetry.MaxEngines-1].name)
}

func TestOpenDefinesEverySimVarAndSubscribes(t *testing.T) {
	fakeSim := msfstest.NewServer(t)
	s := &Sim{Dial: fakeSim.Dial}

	conn, err := s.Open()
	require.NoError(t, err)
	defer conn.Close()

	assert.Equal(t, uint32(requestID), fakeSim.DataRequest())

	want := make([]msfstest.Definition, 0, len(simVars))
	for _, v := range simVars {
		want = append(want, msfstest.Definition{Name: v.name, Units: v.units})
	}
	assert.Equal(t, want, fakeSim.Definitions(), "the data block is read in this order")
	assert.ElementsMatch(t, []string{"Sim", "Pause_EX1"}, fakeSim.Subscriptions())
}

func TestOpenReplyNamesTheSim(t *testing.T) {
	payload := make([]byte, 296)
	copy(payload, "Microsoft Flight Simulator")
	for i, v := range []uint32{11, 0, 282174, 999, 11, 0, 62651, 3} {
		binary.LittleEndian.PutUint32(payload[256+4*i:], v)
	}

	assert.Equal(t, "Microsoft Flight Simulator 11.0 build 282174.999, SimConnect 11.0 build 62651.3", decodeOpen(payload))
	assert.Equal(t, "short open reply", decodeOpen(payload[:100]))
}

func TestOpenFailsWhenThePipeIsNotThere(t *testing.T) {
	s := &Sim{Dial: func() (net.Conn, error) { return nil, errors.New("no pipe") }}

	_, err := s.Open()
	require.ErrorContains(t, err, "MSFS: no pipe")
}

func TestOpenFailsWhileMSFSIsInItsMainMenu(t *testing.T) {
	fakeSim := msfstest.NewServer(t)
	fakeSim.FlightRunning = false
	s := &Sim{Dial: fakeSim.Dial}

	_, err := s.Open()
	require.ErrorContains(t, err, "main menu")
	assert.Empty(t, fakeSim.Definitions(), "nothing is defined on a sim that is not flying")
}

func TestReaderDropsDataWhileNoFlightIsRunning(t *testing.T) {
	fakeSim, updates, _ := startReader(t)
	fakeSim.SendEvent("Sim", 0)
	fakeSim.SendData(requestID, values(49.2, 16.7)...)

	assert.Nil(t, receive(t, updates), "the main menu sends junk, it must not count as live")

	fakeSim.SendEvent("Sim", 1)
	fakeSim.SendData(requestID, values(49.2, 16.7)...)

	var p telemetry.Position
	receive(t, updates)(&p)
	assert.InDelta(t, 49.2, p.Lat, 0.001)
	assert.InDelta(t, 16.7, p.Lon, 0.001)
	assert.Equal(t, telemetry.Kts(250), p.IndicatedAirspeedKt)
	assert.InDelta(t, 4200, p.PayloadKg, 0.001, "payload is total minus empty minus fuel")
	assert.InDelta(t, 7800, p.FuelKg, 0.001)
	assert.Equal(t, 2, p.NumEngines)
	assert.Equal(t, 2, p.EnginesRunning)
	assert.Equal(t, 1, p.GroundSpeedMultiplier)
	assert.False(t, p.Paused)
}

func TestReaderPassesThePauseStateOn(t *testing.T) {
	fakeSim, updates, _ := startReader(t)

	var p telemetry.Position
	fakeSim.SendEvent("Pause_EX1", 4)
	receive(t, updates)(&p)
	assert.True(t, p.Paused, "active pause counts as paused")

	fakeSim.SendData(requestID, values(49.2, 16.7)...)
	receive(t, updates)(&p)
	assert.True(t, p.Paused, "a data block does not forget the pause")

	fakeSim.SendEvent("Pause_EX1", 0)
	receive(t, updates)(&p)
	assert.False(t, p.Paused)
}

func TestReaderEndsTheFlightWhenMSFSQuits(t *testing.T) {
	fakeSim, _, ctx := startReader(t)
	fakeSim.Quit()

	waitForClose(t, ctx.Done(), "MSFS quit but the flight goes on")
}

func TestReaderEndsTheFlightWhenThePipeCloses(t *testing.T) {
	fakeSim, _, ctx := startReader(t)
	fakeSim.Close()

	waitForClose(t, ctx.Done(), "the pipe closed but the flight goes on")
}

func TestResubscribeAsksForDataAgain(t *testing.T) {
	fakeSim := msfstest.NewServer(t)
	s := &Sim{Dial: fakeSim.Dial}

	conn, err := s.Open()
	require.NoError(t, err)
	defer conn.Close()
	fakeSim.DataRequest()

	require.NoError(t, conn.Resubscribe())
	assert.Equal(t, uint32(requestID), fakeSim.DataRequest())
}

func TestPauseSendsPauseOnAndWaitsForMSFSToConfirm(t *testing.T) {
	fakeSim := msfstest.NewServer(t)
	s := &Sim{Dial: fakeSim.Dial}

	result := make(chan error, 1)
	go func() { result <- s.pause(waitTime) }()

	assert.Equal(t, "PAUSE_ON", fakeSim.Transmitted())
	fakeSim.SendEvent("Pause_EX1", 1)

	require.NoError(t, pauseResult(t, result))
}

func TestPauseFailsWhenMSFSDoesNotConfirm(t *testing.T) {
	fakeSim := msfstest.NewServer(t)
	s := &Sim{Dial: fakeSim.Dial}

	result := make(chan error, 1)
	go func() { result <- s.pause(settleTime) }()

	assert.Equal(t, "PAUSE_ON", fakeSim.Transmitted())

	require.ErrorContains(t, pauseResult(t, result), "did not confirm")
}

func startReader(t *testing.T) (*msfstest.Server, chan sim.Update, context.Context) {
	t.Helper()

	fakeSim := msfstest.NewServer(t)
	s := &Sim{Dial: fakeSim.Dial}

	conn, err := s.Open()
	require.NoError(t, err)
	fakeSim.DataRequest()

	ctx, stop := context.WithCancel(t.Context())
	updates := make(chan sim.Update, sim.UpdateBuffer)
	done := make(chan struct{})

	go func() { defer close(done); conn.Run(ctx, updates, stop) }()
	t.Cleanup(func() { stop(); conn.Close(); <-done })

	return fakeSim, updates, ctx
}

func values(lat, lon float64) []float64 {
	v := make([]float64, len(simVars))
	v[latitude], v[longitude] = lat, lon
	v[indicatedAirspeed] = 250.4
	v[totalWeight], v[emptyWeight], v[fuelWeight] = 52000, 40000, 7800
	v[numEngines] = 2
	v[engineCombustionAt], v[engineCombustionAt+1] = 1, 1
	v[simulationRate] = 1
	return v
}

func receive(t *testing.T, updates <-chan sim.Update) sim.Update {
	t.Helper()

	select {
	case u := <-updates:
		return u
	case <-time.After(settleTime):
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

func pauseResult(t *testing.T, result <-chan error) error {
	t.Helper()

	select {
	case err := <-result:
		return err
	case <-time.After(waitTime):
		t.Fatal("pause never returned")
		return nil
	}
}
