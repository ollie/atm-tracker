// Package xplanetest is a stand-in X-Plane: a real UDP socket that answers RREF.
package xplanetest

import (
	"encoding/binary"
	"math"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	replyPrologue   = "RREF,"
	requestPrologue = "RREF\x00"
	requestBytes    = 413
	waitTime        = 2 * time.Second
)

type Update struct {
	Index int32
	Value float32
}

type Subscription struct {
	Freq int32
	Name string
}

type Sim struct {
	Addr string

	t        *testing.T
	conn     *net.UDPConn
	datarefs int

	mu     sync.Mutex
	client *net.UDPAddr
}

func NewSim(t *testing.T, datarefs int) *Sim {
	t.Helper()

	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	return &Sim{Addr: conn.LocalAddr().String(), t: t, conn: conn, datarefs: datarefs}
}

func (s *Sim) Subscriptions() map[int32]Subscription {
	s.t.Helper()

	got := make(map[int32]Subscription, s.datarefs)
	buf := make([]byte, 2048)

	require.NoError(s.t, s.conn.SetReadDeadline(time.Now().Add(waitTime)))

	for range s.datarefs {
		n, from, err := s.conn.ReadFromUDP(buf)
		require.NoError(s.t, err, "the tracker did not subscribe")
		require.Equal(s.t, requestBytes, n, "an RREF request is 413 bytes")
		require.Equal(s.t, requestPrologue, string(buf[0:5]))

		s.mu.Lock()
		s.client = from
		s.mu.Unlock()

		index := int32(binary.LittleEndian.Uint32(buf[9:13]))
		got[index] = Subscription{
			Freq: int32(binary.LittleEndian.Uint32(buf[5:9])),
			Name: CString(buf[13:n]),
		}
	}

	return got
}

func (s *Sim) Send(updates ...Update) {
	s.t.Helper()

	packet := make([]byte, 0, len(replyPrologue)+8*len(updates))
	packet = append(packet, replyPrologue...)

	for _, update := range updates {
		packet = binary.LittleEndian.AppendUint32(packet, uint32(update.Index))
		packet = binary.LittleEndian.AppendUint32(packet, math.Float32bits(update.Value))
	}

	s.write(packet)
}

func (s *Sim) SendRaw(payload string) {
	s.t.Helper()

	s.write([]byte(payload))
}

func (s *Sim) write(packet []byte) {
	s.t.Helper()

	s.mu.Lock()
	client := s.client
	s.mu.Unlock()
	require.NotNil(s.t, client, "the tracker has not subscribed yet")

	_, err := s.conn.WriteToUDP(packet, client)
	require.NoError(s.t, err)
}

func CString(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}

	return string(b)
}
