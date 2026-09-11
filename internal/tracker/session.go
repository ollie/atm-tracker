package tracker

import (
	"context"
	"fmt"
	"log"
	"net"
	"slices"
	"sync"
	"time"

	"github.com/ollie/atm-tracker/internal/api"
	"github.com/ollie/atm-tracker/internal/telemetry"
	"github.com/ollie/atm-tracker/internal/xplane"
)

const windowSize = 100

var terminalStatuses = []string{"completed", "cancelled", "rejected"}

type session struct {
	api         *api.Client
	ui          Display
	onSignedOut func()
	simAddr     string
	timings     Timings
	simTimings  xplane.Timings
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

	switch {
	case ctx.Err() != nil:
		return
	case api.SignedOut(err):
		log.Printf("the game no longer knows this tracker: %v", err)
		s.onSignedOut()
	case err != nil:
		log.Printf("could not read the current flight: %v", err)
		s.ui.SetNote("cannot reach the game")
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
	log.Printf("flying %d, %s to %s", item.ID, item.Departure.Ident, item.Arrival.Ident)

	addr, err := net.ResolveUDPAddr("udp", s.simAddr)
	if err != nil {
		log.Printf("resolve %s: %v", s.simAddr, err)
		return
	}

	conn, err := net.ListenUDP("udp", nil) // nil laddr means 0.0.0.0
	if err != nil {
		log.Printf("listen: %v", err)
		return
	}

	flightCtx, endFlight := context.WithCancel(ctx)
	defer endFlight()

	if err := xplane.Subscribe(conn, addr); err != nil {
		log.Printf("%v", err)
		_ = conn.Close()
		return
	}

	updates := make(chan xplane.Update, xplane.UpdateBuffer)
	snapshots := make(chan xplane.Snapshot, windowSize)

	c := &xplane.Coordinator{
		Updates:     updates,
		Snapshots:   snapshots,
		Resubscribe: func() { logError(xplane.Subscribe(conn, addr)) },
		SetLive:     s.ui.SetLive,
		Timings:     s.simTimings,
	}

	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); xplane.RunReader(flightCtx, conn, updates, endFlight) }()
	go func() { defer wg.Done(); c.Run(flightCtx) }()
	go func() { defer wg.Done(); s.send(flightCtx, item, snapshots, endFlight) }()

	<-flightCtx.Done()
	xplane.Unsubscribe(conn, addr)
	_ = conn.Close() // unblocks RunReader (ReadFromUDP → net.ErrClosed)
	wg.Wait()
}

func (s *session) send(ctx context.Context, item *api.FlightInfo, snapshots <-chan xplane.Snapshot, endFlight context.CancelFunc) {
	pending := make([]telemetry.Position, 0, windowSize)
	sendTicker := time.NewTicker(s.timings.Send)
	defer sendTicker.Stop()

	flush := func() {
		if len(pending) == 0 {
			s.stillFlying(ctx, item, endFlight)
			return
		}

		result, err := s.api.SendPositions(ctx, item.ID, pending)
		switch {
		case ctx.Err() != nil:
			return
		case api.FailedWith(err, "flight_not_active"):
			log.Printf("the game says flight %d is over", item.ID)
			endFlight()
			return
		case api.SignedOut(err):
			log.Printf("the game no longer knows this tracker: %v", err)
			s.onSignedOut()
			endFlight()
			return
		case err != nil:
			log.Printf("send failed, keeping %d positions for retry: %v", len(pending), err)
			s.ui.SetNote(fmt.Sprintf("cannot reach the game, holding %d positions", len(pending)))
			return // leave pending intact - retry next tick
		}

		log.Printf("sent %d positions, flight is %s", len(pending), result.Status)
		pending = pending[:0] // success: clear

		s.ui.SetProgress(result)
		if terminal(result.Status) {
			log.Printf("flight %d is %s", item.ID, result.Status)
			endFlight()
		}
	}

	for {
		select {
		case <-ctx.Done():
			return

		case p := <-snapshots:
			pending = append(pending, p.Data)
			if len(pending) > windowSize {
				pending = pending[1:] // cap: drop oldest unsent
			}
			if p.Flush {
				flush()
			}

		case <-sendTicker.C:
			flush()
		}
	}
}

func (s *session) stillFlying(ctx context.Context, item *api.FlightInfo, endFlight context.CancelFunc) {
	current, err := s.api.Flight(ctx)

	switch {
	case ctx.Err() != nil:
	case api.SignedOut(err):
		log.Printf("the game no longer knows this tracker: %v", err)
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
