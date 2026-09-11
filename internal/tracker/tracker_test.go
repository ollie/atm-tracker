package tracker

import (
	"io"
	"log"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/ollie/atm-tracker/internal/api"
	"github.com/ollie/atm-tracker/internal/ui"
	"github.com/stretchr/testify/assert"
)

const waitTime = 2 * time.Second

var _ Display = (*ui.UI)(nil)

func TestMain(m *testing.M) {
	log.SetOutput(io.Discard)
	os.Exit(m.Run())
}

type spyDisplay struct {
	mu       sync.Mutex
	live     bool
	note     string
	progress *api.PositionsResult
	flight   *api.FlightInfo
	noFlight bool
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

func (d *spyDisplay) SetLive(live bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.live = live
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

func (d *spyDisplay) Live() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.live
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
	for _, status := range terminalStatuses {
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
