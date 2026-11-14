package tracker

import (
	"context"
	"fmt"
	"log"
	"slices"
	"sync"
	"time"

	"github.com/ollie/atm-tracker/internal/api"
	"github.com/ollie/atm-tracker/internal/sim"
	"github.com/ollie/atm-tracker/internal/telemetry"
)

const windowSize = 100

var terminalStatuses = []string{"completed", "diverted", "cancelled", "rejected"}

type session struct {
	api           *api.Client
	ui            Display
	onSignedOut   func()
	onUnsupported func(minVersion string)
	sim           sim.Sim
	timings       Timings
	simTimings    sim.Timings
	deboards      <-chan struct{}
	snapshots     chan telemetry.Position
}

func (s *session) run(ctx context.Context) {
	for ctx.Err() == nil {
		s.poll(ctx)
		s.waitForNextPoll(ctx)
	}
}

func (s *session) waitForNextPoll(ctx context.Context) {
	nextPoll := time.After(s.timings.FlightPoll)

	for {
		select {
		case <-ctx.Done():
			return
		case <-nextPoll:
			return
		case p := <-s.snapshots:
			s.showActual(p)
		}
	}
}

func (s *session) showActual(p telemetry.Position) {
	s.ui.SetActual(p.PayloadKg, p.FuelKg, p.EnginesRunning)
}

func (s *session) setLive(ctx context.Context, live bool) {
	if ctx.Err() == nil {
		s.ui.SetLive(s.sim.Name(), live)
	}
}

func (s *session) readSim(ctx context.Context) {
	lastOpenError := ""

	for ctx.Err() == nil {
		conn, err := s.sim.Open()
		switch {
		case err == nil:
			lastOpenError = ""
			s.readConnection(ctx, conn)
		case err.Error() != lastOpenError:
			log.Printf("%v", err)
			lastOpenError = err.Error()
		}

		select {
		case <-ctx.Done():
		case <-time.After(s.timings.FlightPoll):
		}
	}
}

func (s *session) readConnection(ctx context.Context, conn sim.Connection) {
	connCtx, closeConn := context.WithCancel(ctx)
	defer closeConn()

	updates := make(chan sim.Update, sim.UpdateBuffer)

	c := &sim.Coordinator{
		Updates:     updates,
		Snapshots:   s.snapshots,
		Resubscribe: func() { logError(conn.Resubscribe()) },
		SetLive:     func(live bool) { s.setLive(ctx, live) },
		Timings:     s.simTimings,
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); conn.Run(connCtx, updates, closeConn) }()
	go func() { defer wg.Done(); c.Run(connCtx) }()

	<-connCtx.Done()
	conn.Close()
	wg.Wait()
	s.setLive(ctx, false)
}

func (s *session) discardQueuedSnapshots() {
	for {
		select {
		case <-s.snapshots:
		default:
			return
		}
	}
}

func (s *session) poll(ctx context.Context) {
	item, err := s.api.Flight(ctx)
	minVersion, refusal := api.Unsupported(err)

	switch {
	case ctx.Err() != nil:
		return
	case refusal:
		s.onUnsupported(minVersion)
	case api.SignedOut(err):
		log.Printf("the API no longer knows this tracker: %v", err)
		s.onSignedOut()
	case err != nil:
		log.Printf("could not read the current flight: %v", err)
		s.ui.SetNote("cannot reach the Air Transport Magnate website")
	case item == nil:
		s.ui.SetNoFlight()
		s.ui.SetNote("")
	default:
		s.ui.SetNote("")
		s.fly(ctx, item)
		s.ui.SetNoFlight()
	}
}

func (s *session) fly(ctx context.Context, item *api.FlightInfo) {
	s.ui.StartFlight(item)
	log.Printf("flying %d, %s to %s", item.ID, item.Departure.Code, item.Arrival.Code)

	s.discardQueuedSnapshots()

	flightCtx, endFlight := context.WithCancel(ctx)
	defer endFlight()

	s.send(flightCtx, item, endFlight)
}

func (s *session) send(ctx context.Context, item *api.FlightInfo, endFlight context.CancelFunc) {
	wasPaused := false
	pending := make([]telemetry.Position, 0, windowSize)
	sendTicker := time.NewTicker(s.timings.Send)
	defer sendTicker.Stop()

	flush := func() {
		if len(pending) == 0 {
			s.stillFlying(ctx, item, endFlight)
			return
		}

		result, err := s.api.SendPositions(ctx, item.ID, pending)
		minVersion, refusal := api.Unsupported(err)
		switch {
		case ctx.Err() != nil:
			return
		case refusal:
			s.onUnsupported(minVersion)
			endFlight()
			return
		case api.FailedWith(err, "flight_not_active"):
			log.Printf("the API says flight %d is over", item.ID)
			endFlight()
			return
		case api.SignedOut(err):
			log.Printf("the API no longer knows this tracker: %v", err)
			s.onSignedOut()
			endFlight()
			return
		case api.FailedWith(err, "no_airport_at_landing_site"):
			log.Printf("the API stored %d positions but found no airport under flight %d", len(pending), item.ID)
			pending = pending[:0]
			s.ui.SetNote("No airport here, taxi closer to the apron and press Deboard again")
			return
		case err != nil:
			log.Printf("send failed, keeping %d positions for retry: %v", len(pending), err)
			s.ui.SetNote(fmt.Sprintf("cannot reach the Air Transport Magnate website, holding %d positions", len(pending)))
			return // leave pending intact - retry next tick
		}

		log.Printf("sent %d positions, flight is %s", len(pending), result.Status)
		simPaused := pending[len(pending)-1].Paused
		pending = pending[:0] // success: clear
		if result.PauseRequested && !simPaused {
			go s.pauseSim()
		}
		s.progress(result, item, endFlight)
	}

	for {
		select {
		case <-ctx.Done():
			return

		case p := <-s.snapshots:
			s.showActual(p)

			sendNow := p.Paused != wasPaused || p.Crashed
			if p.Paused && !sendNow {
				continue
			}
			wasPaused = p.Paused

			log.Printf("Collected: %+v", p)
			pending = append(pending, p)
			if len(pending) > windowSize {
				pending = pending[1:] // cap: drop oldest unsent
			}
			if sendNow {
				flush()
			}

		case <-sendTicker.C:
			flush()

		case <-s.deboards:
			flush()
			if ctx.Err() != nil {
				continue
			}
			if len(pending) > 0 {
				s.ui.SetNote("cannot reach the Air Transport Magnate website, press Deboard again once it is back")
				continue
			}
			s.deboard(ctx, item, endFlight)
		}
	}
}

func (s *session) deboard(ctx context.Context, item *api.FlightInfo, endFlight context.CancelFunc) {
	result, err := s.api.Deboard(ctx, item.ID)
	minVersion, refusal := api.Unsupported(err)

	switch {
	case ctx.Err() != nil:
	case refusal:
		s.onUnsupported(minVersion)
		endFlight()
	case api.FailedWith(err, "flight_not_deboarding"):
		log.Printf("the API would not deboard flight %d: %v", item.ID, err)
		s.stillFlying(ctx, item, endFlight)
	case api.FailedWith(err, "no_airport_at_landing_site"):
		log.Printf("the API found no airport under flight %d", item.ID)
		s.ui.SetNote("No airport here, taxi closer to the apron and press Deboard again")
	case api.SignedOut(err):
		log.Printf("the API no longer knows this tracker: %v", err)
		s.onSignedOut()
		endFlight()
	case err != nil:
		log.Printf("deboard failed: %v", err)
		s.ui.SetNote(fmt.Sprintf("could not deboard: %v", err))
	default:
		log.Printf("deboarded, flight is %s", result.Status)
		s.progress(result, item, endFlight)
	}
}

func (s *session) progress(result *api.PositionsResult, item *api.FlightInfo, endFlight context.CancelFunc) {
	s.ui.SetProgress(result)
	if terminal(result.Status) {
		log.Printf("flight %d is %s", item.ID, result.Status)
		endFlight()
	}
}

func (s *session) pauseSim() {
	log.Print("the website asked for a pause, pausing the sim")
	if err := s.sim.Pause(); err != nil {
		log.Printf("could not pause the sim: %v", err)
	}
}

func (s *session) stillFlying(ctx context.Context, item *api.FlightInfo, endFlight context.CancelFunc) {
	current, err := s.api.Flight(ctx)
	minVersion, refusal := api.Unsupported(err)

	switch {
	case ctx.Err() != nil:
	case refusal:
		s.onUnsupported(minVersion)
		endFlight()
	case api.SignedOut(err):
		log.Printf("the API no longer knows this tracker: %v", err)
		s.onSignedOut()
		endFlight()
	case err != nil:
		log.Printf("could not read the current flight: %v", err)
	case current == nil || current.ID != item.ID:
		log.Printf("flight %d is no longer the one to fly", item.ID)
		endFlight()
	default:
		s.ui.SetFlight(current)
	}
}

func terminal(status string) bool {
	return slices.Contains(terminalStatuses, status)
}
