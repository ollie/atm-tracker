package xplane

import (
	"context"
	"fmt"
	"net"

	"github.com/ollie/atm-tracker/internal/sim"
)

type Sim struct {
	Addr string
}

func (s *Sim) Name() string {
	return "X-Plane"
}

func (s *Sim) Open() (sim.Connection, error) {
	addr, err := net.ResolveUDPAddr("udp", s.Addr)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", s.Addr, err)
	}

	conn, err := net.ListenUDP("udp", nil) // nil laddr means 0.0.0.0
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}

	if err := subscribe(conn, addr); err != nil {
		_ = conn.Close()
		return nil, err
	}

	return &connection{conn: conn, addr: addr}, nil
}

func (s *Sim) Pause() error {
	return pause(s.Addr, pauseSettle)
}

type connection struct {
	conn *net.UDPConn
	addr *net.UDPAddr
}

func (c *connection) Resubscribe() error {
	return subscribe(c.conn, c.addr)
}

func (c *connection) Run(ctx context.Context, updates chan<- sim.Update, stop context.CancelFunc) {
	runReader(ctx, c.conn, updates, stop)
}

func (c *connection) Close() {
	unsubscribe(c.conn, c.addr)
	_ = c.conn.Close() // unblocks runReader (ReadFromUDP → net.ErrClosed)
}
