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
}

func (s *session) run(ctx context.Context) {
	for {
		s.poll(ctx)

		select {
		case <-ctx.Done():
			return
		case <-time.After(s.timings.FlightPoll):
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

	conn, err := s.sim.Open()
	if err != nil {
		log.Printf("%v", err)
		return
	}

	flightCtx, endFlight := context.WithCancel(ctx)
	defer endFlight()

	updates := make(chan sim.Update, sim.UpdateBuffer)
	snapshots := make(chan sim.Snapshot, windowSize)

	c := &sim.Coordinator{
		Updates:     updates,
		Snapshots:   snapshots,
		Resubscribe: func() { logError(conn.Resubscribe()) },
		SetLive:     func(live bool) { s.ui.SetLive(s.sim.Name(), live) },
		Timings:     s.simTimings,
	}

	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); conn.Run(flightCtx, updates, endFlight) }()
	go func() { defer wg.Done(); c.Run(flightCtx) }()
	go func() { defer wg.Done(); s.send(flightCtx, item, snapshots, endFlight) }()

	<-flightCtx.Done()
	conn.Close()
	wg.Wait()
}

func (s *session) send(ctx context.Context, item *api.FlightInfo, snapshots <-chan sim.Snapshot, endFlight context.CancelFunc) {
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

		case p := <-snapshots:
			pending = append(pending, p.Data)
			s.ui.SetActual(p.Data.PayloadKg, p.Data.FuelKg, p.Data.EnginesRunning)
			if len(pending) > windowSize {
				pending = pending[1:] // cap: drop oldest unsent
			}
			if p.Flush {
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
