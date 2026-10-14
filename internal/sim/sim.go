// Package sim is what every simulator looks like to the tracker, and the cadence it is read at.
package sim

import (
	"context"

	"github.com/ollie/atm-tracker/internal/telemetry"
)

const UpdateBuffer = 64

type Update func(*telemetry.Position)

type Sim interface {
	Name() string
	Open() (Connection, error)
	Pause() error
}

type Connection interface {
	Resubscribe() error
	Run(ctx context.Context, updates chan<- Update, stop context.CancelFunc)
	Close()
}
