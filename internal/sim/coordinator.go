package sim

import (
	"context"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/ollie/atm-tracker/internal/telemetry"
)

const (
	snapshotEvery    = 2 * time.Second
	silenceTimeout   = 5 * time.Second
	resubscribeEvery = 5 * time.Second
)

type Timings struct {
	Snapshot    time.Duration
	Silence     time.Duration
	Resubscribe time.Duration
}

func DefaultTimings() Timings {
	return Timings{
		Snapshot:    snapshotEvery,
		Silence:     silenceTimeout,
		Resubscribe: resubscribeEvery,
	}
}

type Coordinator struct {
	Updates     <-chan Update
	Snapshots   chan<- telemetry.Position
	Resubscribe func()
	SetLive     func(bool)
	Timings     Timings
}

func (c *Coordinator) Run(ctx context.Context) {
	snapshotTicker := time.NewTicker(c.Timings.Snapshot)
	defer snapshotTicker.Stop()
	silenceTicker := time.NewTicker(c.Timings.Resubscribe)
	defer silenceTicker.Stop()

	var posData telemetry.Position
	var lastPacket time.Time
	live, silenceLogged := false, false
	c.SetLive(false)

	for {
		select {
		case <-ctx.Done():
			return

		case u := <-c.Updates:
			lastPacket = time.Now()
			if !live {
				live, silenceLogged = true, false
				c.SetLive(true)
			}

			u(&posData)

		case <-silenceTicker.C:
			if time.Since(lastPacket) < c.Timings.Silence {
				continue
			}
			if live {
				live = false
				c.SetLive(false)
			}
			if !silenceLogged {
				log.Printf("no sim data for %s, re-subscribing until it answers", c.Timings.Silence)
				silenceLogged = true
			}
			c.Resubscribe()

		case <-snapshotTicker.C:
			if !live {
				continue
			}

			posData.UUID = uuid.NewString()
			posData.ReportedAt = time.Now().UTC()

			snapshot := posData
			posData.Crashed = false

			select {
			case c.Snapshots <- snapshot:
			case <-ctx.Done():
				return
			}
		}
	}
}
