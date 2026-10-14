// Package msfs speaks SimConnect over its named pipe and turns what it hears into samples.
package msfs

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/ollie/atm-tracker/internal/sim"
	"github.com/ollie/atm-tracker/internal/telemetry"
)

const (
	PipeName = `\\.\pipe\Microsoft Flight Simulator\SimConnect`

	appName     = "ATM Tracker"
	dialTimeout = 2 * time.Second
	openTimeout = 5 * time.Second
	pauseSettle = time.Second

	definitionID   = 1
	requestID      = 1
	stateRequestID = 2

	pauseEvent   = 1
	simEvent     = 2
	pauseOnEvent = 3

	pauseEventName   = "Pause_EX1"
	simEventName     = "Sim"
	pauseOnEventName = "PAUSE_ON"
)

type Sim struct {
	Dial func() (net.Conn, error)
}

func (s *Sim) Name() string {
	return "MSFS"
}

func (s *Sim) Open() (sim.Connection, error) {
	c, err := s.open()
	if err != nil {
		return nil, err
	}

	inFlight, err := c.flightRunning()
	if err != nil {
		_ = c.conn.Close()
		return nil, err
	}
	if !inFlight {
		_ = c.conn.Close()
		return nil, errors.New("MSFS is in its main menu")
	}

	for _, v := range simVars {
		if err := c.send(addToDataDefinition(definitionID, v.name, v.units)); err != nil {
			_ = c.conn.Close()
			return nil, fmt.Errorf("define %s: %w", v.name, err)
		}
	}

	for _, p := range []*packet{
		requestDataEverySecond(requestID, definitionID),
		subscribeToSystemEvent(simEvent, simEventName),
		subscribeToSystemEvent(pauseEvent, pauseEventName),
	} {
		if err := c.send(p); err != nil {
			_ = c.conn.Close()
			return nil, err
		}
	}

	return c, nil
}

func (s *Sim) Pause() error {
	return s.pause(pauseSettle)
}

func (s *Sim) pause(settle time.Duration) error {
	c, err := s.open()
	if err != nil {
		return err
	}
	defer func() { _ = c.conn.Close() }()

	for _, p := range []*packet{
		subscribeToSystemEvent(pauseEvent, pauseEventName),
		mapClientEventToSimEvent(pauseOnEvent, pauseOnEventName),
		transmitClientEvent(pauseOnEvent),
	} {
		if err := c.send(p); err != nil {
			return err
		}
	}

	_ = c.conn.SetReadDeadline(time.Now().Add(settle))
	for {
		m, err := readMessage(c.conn)
		if err != nil {
			return fmt.Errorf("MSFS did not confirm the pause: %w", err)
		}
		if m.kind != recvEvent {
			continue
		}
		if event, data, err := decodeEvent(m.payload); err == nil && event == pauseEvent && data != 0 {
			return nil
		}
	}
}

func (s *Sim) open() (*connection, error) {
	conn, err := s.Dial()
	if err != nil {
		return nil, fmt.Errorf("MSFS: %w", err)
	}

	c := &connection{conn: conn}
	_ = conn.SetReadDeadline(time.Now().Add(openTimeout))

	if err = c.send(openPacket(appName)); err != nil {
		_ = conn.Close()
		return nil, err
	}

	m, err := readMessage(conn)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("MSFS did not answer the open: %w", err)
	}
	if m.kind != recvOpen {
		_ = conn.Close()
		return nil, fmt.Errorf("MSFS answered the open with message %d", m.kind)
	}

	_ = conn.SetReadDeadline(time.Time{})
	return c, nil
}

type connection struct {
	conn net.Conn

	mu     sync.Mutex
	sendID uint32
}

func (c *connection) send(p *packet) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	_, err := c.conn.Write(p.bytes(c.sendID))
	c.sendID++
	return err
}

func (c *connection) flightRunning() (bool, error) {
	if err := c.send(systemStateRequest(stateRequestID, simEventName)); err != nil {
		return false, err
	}

	_ = c.conn.SetReadDeadline(time.Now().Add(openTimeout))
	defer func() { _ = c.conn.SetReadDeadline(time.Time{}) }()

	for {
		m, err := readMessage(c.conn)
		if err != nil {
			return false, fmt.Errorf("MSFS did not tell whether a flight is running: %w", err)
		}
		if m.kind != recvSystemState {
			continue
		}
		if request, value, err := decodeSystemState(m.payload); err == nil && request == stateRequestID {
			return value == 1, nil
		}
	}
}

func (c *connection) Resubscribe() error {
	return c.send(requestDataEverySecond(requestID, definitionID))
}

func (c *connection) Run(ctx context.Context, updates chan<- sim.Update, stop context.CancelFunc) {
	inFlight, simPaused, dataLogged := true, false, false

	for {
		m, err := readMessage(c.conn)
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("MSFS read failed: %v", err)
			}
			stop()
			return
		}

		var update sim.Update
		switch m.kind {
		case recvSimObjectData:
			values, err := decodeSimObjectData(m.payload, requestID, len(simVars))
			if err != nil {
				if !dataLogged {
					log.Printf("MSFS %v", err)
					dataLogged = true
				}
				continue
			}
			update = dataUpdate(values, simPaused)
		case recvEvent:
			event, data, err := decodeEvent(m.payload)
			if err != nil {
				continue
			}
			switch event {
			case simEvent:
				inFlight = data == 1
				continue
			case pauseEvent:
				simPaused = data != 0
				update = pauseUpdate(simPaused)
			default:
				continue
			}
		case recvQuit:
			log.Print("MSFS quit")
			stop()
			return
		case recvException:
			log.Printf("MSFS %s", decodeException(m.payload))
			continue
		default:
			continue
		}

		if !inFlight {
			continue
		}

		select {
		case updates <- update:
		case <-ctx.Done():
			return
		}
	}
}

func (c *connection) Close() {
	_ = c.conn.Close()
}

func dataUpdate(values []float64, simPaused bool) sim.Update {
	return func(p *telemetry.Position) {
		applyData(p, values)
		p.Paused = simPaused
	}
}

func pauseUpdate(simPaused bool) sim.Update {
	return func(p *telemetry.Position) { p.Paused = simPaused }
}
