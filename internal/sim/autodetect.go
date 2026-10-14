package sim

import (
	"context"
	"errors"
	"sync"
)

type Autodetect struct {
	Sims []Sim

	mu      sync.Mutex
	current Sim
}

func (a *Autodetect) Name() string {
	if s := a.currentSim(); s != nil {
		return s.Name()
	}

	return "–"
}

func (a *Autodetect) Open() (Connection, error) {
	if s := a.currentSim(); s != nil {
		return s.Open()
	}

	var errs []error
	for i, s := range a.Sims {
		conn, err := s.Open()
		if err != nil {
			errs = append(errs, err)
			continue
		}

		return &candidateConnection{Connection: conn, autodetect: a, sim: s, preferred: a.Sims[:i]}, nil
	}

	return nil, errors.Join(errs...)
}

func (a *Autodetect) Pause() error {
	if s := a.currentSim(); s != nil {
		return s.Pause()
	}

	var errs []error
	for _, s := range a.Sims {
		err := s.Pause()
		if err == nil {
			a.selectSim(s)
			return nil
		}
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}

func (a *Autodetect) currentSim() Sim {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.current
}

func (a *Autodetect) selectSim(s Sim) {
	a.mu.Lock()
	a.current = s
	a.mu.Unlock()
}

type candidateConnection struct {
	Connection
	autodetect *Autodetect
	sim        Sim
	preferred  []Sim
}

func (c *candidateConnection) Resubscribe() error {
	if c.autodetect.currentSim() == nil {
		for _, s := range c.preferred {
			if probe, err := s.Open(); err == nil {
				probe.Close()
				c.Close()
				return nil
			}
		}
	}

	return c.Connection.Resubscribe()
}

func (c *candidateConnection) Run(ctx context.Context, updates chan<- Update, stop context.CancelFunc) {
	inner := make(chan Update, cap(updates))
	go func() {
		c.Connection.Run(ctx, inner, stop)
		close(inner)
	}()

	for update := range inner {
		c.autodetect.selectSim(c.sim)
		select {
		case updates <- update:
		case <-ctx.Done():
			return
		}
	}
}
