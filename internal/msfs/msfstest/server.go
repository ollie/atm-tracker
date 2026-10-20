// Package msfstest is a stand-in MSFS: a SimConnect server on an in-memory pipe.
package msfstest

import (
	"encoding/binary"
	"io"
	"math"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	requestOpen                   = 0x01
	requestMapClientEventToSim    = 0x04
	requestTransmitClientEvent    = 0x05
	requestAddToDataDefinition    = 0x0c
	requestDataOnSimObject        = 0x0e
	requestSubscribeToSystemEvent = 0x17
	requestSystemState            = 0x35

	recvOpen          = 2
	recvQuit          = 3
	recvEvent         = 4
	recvSimObjectData = 8
	recvSystemState   = 15

	protocol = 5
	waitTime = 2 * time.Second
)

type Definition struct {
	Name  string
	Units string
}

type Server struct {
	FlightRunning bool

	t *testing.T

	mu            sync.Mutex
	conn          net.Conn
	definitions   []Definition
	subscriptions map[string]uint32
	clientEvents  map[uint32]string
	transmitted   chan string
	dataRequests  chan uint32
}

func NewServer(t *testing.T) *Server {
	t.Helper()

	return &Server{
		FlightRunning: true,
		t:             t,
		subscriptions: map[string]uint32{},
		clientEvents:  map[uint32]string{},
		transmitted:   make(chan string, 16),
		dataRequests:  make(chan uint32, 16),
	}
}

func (s *Server) Dial() (net.Conn, error) {
	client, server := net.Pipe()

	s.mu.Lock()
	s.conn = server
	s.mu.Unlock()

	s.t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	go s.serve(server)

	return client, nil
}

func (s *Server) serve(conn net.Conn) {
	for {
		var header [16]byte
		if _, err := io.ReadFull(conn, header[:]); err != nil {
			return
		}

		size := binary.LittleEndian.Uint32(header[0:4])
		payload := make([]byte, size-16)

		s.mu.Lock()
		if _, err := io.ReadFull(conn, payload); err != nil {
			s.mu.Unlock()
			return
		}

		kind := binary.LittleEndian.Uint32(header[8:12]) & 0x0fffffff

		switch kind {
		case requestOpen:
			s.write(conn, recvOpen, make([]byte, 296))
		case requestAddToDataDefinition:
			s.definitions = append(s.definitions, Definition{Name: CString(payload[4:260]), Units: CString(payload[260:516])})
		case requestSubscribeToSystemEvent:
			s.subscriptions[CString(payload[4:260])] = binary.LittleEndian.Uint32(payload[0:4])
		case requestMapClientEventToSim:
			s.clientEvents[binary.LittleEndian.Uint32(payload[0:4])] = CString(payload[4:260])
		case requestTransmitClientEvent:
			s.transmitted <- s.clientEvents[binary.LittleEndian.Uint32(payload[4:8])]
		case requestDataOnSimObject:
			s.dataRequests <- binary.LittleEndian.Uint32(payload[0:4])
		case requestSystemState:
			state := make([]byte, 0, 268)
			state = append(state, payload[0:4]...)
			running := uint32(0)
			if s.FlightRunning {
				running = 1
			}
			state = binary.LittleEndian.AppendUint32(state, running)
			s.write(conn, recvSystemState, append(state, make([]byte, 264)...))
		}
		s.mu.Unlock()
	}
}

func (s *Server) Definitions() []Definition {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.definitions
}

func (s *Server) Subscriptions() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	names := make([]string, 0, len(s.subscriptions))
	for name := range s.subscriptions {
		names = append(names, name)
	}
	return names
}

func (s *Server) DataRequest() uint32 {
	s.t.Helper()

	select {
	case request := <-s.dataRequests:
		return request
	case <-time.After(waitTime):
		s.t.Fatal("the tracker did not ask for data")
		return 0
	}
}

func (s *Server) Transmitted() string {
	s.t.Helper()

	select {
	case name := <-s.transmitted:
		return name
	case <-time.After(waitTime):
		s.t.Fatal("the tracker did not transmit an event")
		return ""
	}
}

func (s *Server) SendData(request uint32, values ...float64) {
	s.t.Helper()

	payload := make([]byte, 0, 28+8*len(values))
	payload = binary.LittleEndian.AppendUint32(payload, request)
	payload = binary.LittleEndian.AppendUint32(payload, 0)
	payload = binary.LittleEndian.AppendUint32(payload, 1)
	payload = binary.LittleEndian.AppendUint32(payload, 0)
	payload = binary.LittleEndian.AppendUint32(payload, 1)
	payload = binary.LittleEndian.AppendUint32(payload, 1)
	payload = binary.LittleEndian.AppendUint32(payload, uint32(len(values)))
	for _, v := range values {
		payload = binary.LittleEndian.AppendUint64(payload, math.Float64bits(v))
	}

	s.send(recvSimObjectData, payload)
}

func (s *Server) SendEvent(name string, data uint32) {
	s.t.Helper()

	s.mu.Lock()
	event, ok := s.subscriptions[name]
	s.mu.Unlock()
	require.Truef(s.t, ok, "the tracker never subscribed to %s", name)

	payload := make([]byte, 0, 12)
	payload = binary.LittleEndian.AppendUint32(payload, 0xffffffff)
	payload = binary.LittleEndian.AppendUint32(payload, event)
	payload = binary.LittleEndian.AppendUint32(payload, data)

	s.send(recvEvent, payload)
}

func (s *Server) Quit() {
	s.t.Helper()
	s.send(recvQuit, nil)
}

func (s *Server) Close() {
	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()
	_ = conn.Close()
}

func (s *Server) send(kind uint32, payload []byte) {
	s.t.Helper()

	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()
	require.NotNil(s.t, conn, "the tracker has not dialled yet")

	s.write(conn, kind, payload)
}

func (s *Server) write(conn net.Conn, kind uint32, payload []byte) {
	s.t.Helper()

	packet := make([]byte, 0, 12+len(payload))
	packet = binary.LittleEndian.AppendUint32(packet, uint32(12+len(payload)))
	packet = binary.LittleEndian.AppendUint32(packet, protocol)
	packet = binary.LittleEndian.AppendUint32(packet, kind)
	packet = append(packet, payload...)

	assert.NoError(s.t, conn.SetWriteDeadline(time.Now().Add(waitTime)))
	_, err := conn.Write(packet)
	assert.NoError(s.t, err)
}

func CString(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}

	return string(b)
}
