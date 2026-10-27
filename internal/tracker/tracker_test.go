package tracker

import (
	"io"
	"log"
	"os"
	"sync"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"github.com/ollie/atm-tracker/internal/api"
	"github.com/ollie/atm-tracker/internal/api/apitest"
	"github.com/ollie/atm-tracker/internal/ui"
	"github.com/stretchr/testify/assert"
	"golang.org/x/oauth2"
)

const waitTime = 2 * time.Second

var _ Display = (*ui.UI)(nil)

func TestMain(m *testing.M) {
	log.SetOutput(io.Discard)
	os.Exit(m.Run())
}

type actualLoad struct {
	payloadKg      float32
	fuelKg         float32
	enginesRunning int
}

type spyDisplay struct {
	mu               sync.Mutex
	simName          string
	live             bool
	actual           actualLoad
	note             string
	progress         *api.PositionsResult
	flight           *api.FlightInfo
	noFlight         bool
	minVersionWanted string
	latestVersion    string
}

func (d *spyDisplay) SigningIn() {}
func (d *spyDisplay) SignedIn()  {}
func (d *spyDisplay) SignedOut() {}

func (d *spyDisplay) SetNoFlight() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.noFlight = true
}

func (d *spyDisplay) StartFlight(item *api.FlightInfo) {
	d.SetFlight(item)
}

func (d *spyDisplay) SetFlight(item *api.FlightInfo) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.flight = item
}

func (d *spyDisplay) SetLive(simName string, live bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.simName, d.live = simName, live
}

func (d *spyDisplay) SetActual(payloadKg, fuelKg float32, enginesRunning int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.actual = actualLoad{payloadKg: payloadKg, fuelKg: fuelKg, enginesRunning: enginesRunning}
}

func (d *spyDisplay) SetNote(note string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.note = note
}

func (d *spyDisplay) SetProgress(result *api.PositionsResult) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.progress = result
}

func (d *spyDisplay) Unsupported(minVersion string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.minVersionWanted = minVersion
}

func (d *spyDisplay) MinVersionWanted() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.minVersionWanted
}

func (d *spyDisplay) UpdateAvailable(latestVersion string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.latestVersion = latestVersion
}

func (d *spyDisplay) LatestVersion() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.latestVersion
}

func (d *spyDisplay) Live() (simName string, live bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.simName, d.live
}

func (d *spyDisplay) Actual() actualLoad {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.actual
}

func (d *spyDisplay) Note() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.note
}

func (d *spyDisplay) Progress() *api.PositionsResult {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.progress
}

func TestTerminal(t *testing.T) {
	for _, status := range []string{"completed", "diverted", "cancelled", "rejected"} {
		assert.Truef(t, terminal(status), "%s should end the flight", status)
	}

	flying := []string{"on_ground", "boarding", "boarded", "taxi", "departure", "enroute", "approach", "go_around", "landed", "taxi_to_gate"}
	for _, status := range flying {
		assert.Falsef(t, terminal(status), "%s should keep the flight going", status)
	}
}

func TestWaitForReturnsWhenTheWorkFinishes(t *testing.T) {
	var wg sync.WaitGroup

	wg.Add(1)
	go func() { defer wg.Done(); time.Sleep(10 * time.Millisecond) }()

	assert.True(t, waitFor(&wg, waitTime), "gave up on work that finished in time")
}

func TestWaitForGivesUp(t *testing.T) {
	var wg sync.WaitGroup
	done := make(chan struct{})

	wg.Add(1)
	go func() { defer wg.Done(); <-done }()
	defer close(done)

	assert.False(t, waitFor(&wg, 10*time.Millisecond), "claimed work had finished while it was still running")
}

func TestASignedOutTrackerLearnsAtStartUpThatItIsUnsupported(t *testing.T) {
	server := apitest.NewServer(t)
	server.Fail(upgradeRequired)
	display := &spyDisplay{}

	tr := New(test.NewApp(), display, "0.0.12")
	tr.base = server.URL
	tr.Start()

	assert.Eventually(t, func() bool { return display.MinVersionWanted() == "0.0.13" }, waitTime, time.Millisecond, "the player would log in only to be refused")
}

func TestATrackerWithADeadTokenLearnsAtStartUpThatItIsUnsupported(t *testing.T) {
	server := apitest.NewServer(t)
	server.Fail(upgradeRequired)
	display := &spyDisplay{}

	tr := New(test.NewApp(), display, "0.0.12")
	tr.base = server.URL
	tr.store.Save(&oauth2.Token{AccessToken: "expired", RefreshToken: "revoked", Expiry: time.Now().Add(-time.Hour)})
	tr.Start()

	assert.Eventually(t, func() bool { return display.MinVersionWanted() == "0.0.13" }, waitTime, time.Millisecond, "the refresh fails before any request reaches the API, so only the start-up check can tell")
}

func TestStartUpOffersOnlyANewerTracker(t *testing.T) {
	server := apitest.NewServer(t)
	server.SetLatestVersion("0.0.10")

	for _, c := range []struct{ version, offeredVersion string }{
		{"0.0.9", "0.0.10"},
		{"0.0.10", ""},
		{"0.0.11", ""},
	} {
		display := &spyDisplay{}
		tr := New(test.NewApp(), display, c.version)
		tr.base = server.URL

		tr.checkVersion()

		assert.Equalf(t, c.offeredVersion, display.LatestVersion(), "tracker v%s", c.version)
	}
}
