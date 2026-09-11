package xplane

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

type Snapshot struct {
	Data  telemetry.Position
	Flush bool
}

type Coordinator struct {
	Updates     <-chan Update
	Snapshots   chan<- Snapshot
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
	live, wasPaused := false, false
	c.SetLive(false)

	for {
		select {
		case <-ctx.Done():
			return

		case u := <-c.Updates:
			lastPacket = time.Now()
			if !live {
				live = true
				c.SetLive(true)
			}

			item, ok := datarefsMap[u.Index]
			if !ok {
				log.Printf("unknown dataref at index %d", u.Index)
				continue
			}
			item.assign(&posData, u.Value)

		case <-silenceTicker.C:
			if time.Since(lastPacket) < c.Timings.Silence {
				continue
			}
			if live {
				live = false
				c.SetLive(false)
			}
			log.Printf("no sim data for %s, re-subscribing", c.Timings.Silence)
			c.Resubscribe()

		case <-snapshotTicker.C:
			if !live {
				continue
			}

			flush := posData.Paused != wasPaused
			if posData.Paused && !flush {
				continue
			}
			wasPaused = posData.Paused

			posData.UUID = uuid.NewString()
			posData.ReportedAt = time.Now().UTC()
			log.Printf("Collected: %+v", posData)

			select {
			case c.Snapshots <- Snapshot{Data: posData, Flush: flush}:
			case <-ctx.Done():
				return
			}
		}
	}
}
