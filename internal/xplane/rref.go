package xplane

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"math"
	"net"
	"os"
	"time"
)

const (
	readPoll       = 1 * time.Second
	cleanupTimeout = 1 * time.Second
	subscribeHz    = 1
	requestBytes   = 413
	replyPrologue  = "RREF"
	UpdateBuffer   = 64
)

func Subscribe(conn *net.UDPConn, addr *net.UDPAddr) error {
	for idx, dataref := range datarefsMap {
		if _, err := conn.WriteToUDP(makeRREF(subscribeHz, idx, dataref.name), addr); err != nil {
			return fmt.Errorf("subscribe %s: %w", dataref.name, err)
		}
	}

	return nil
}

func Unsubscribe(conn *net.UDPConn, addr *net.UDPAddr) {
	_ = conn.SetWriteDeadline(time.Now().Add(cleanupTimeout))
	for idx, dataref := range datarefsMap {
		_, _ = conn.WriteToUDP(makeRREF(0, idx, dataref.name), addr)
	}
}

func RunReader(ctx context.Context, conn *net.UDPConn, updates chan<- Update, stop context.CancelFunc) {
	buf := make([]byte, 2048)

	for {
		_ = conn.SetReadDeadline(time.Now().Add(readPoll))

		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				select {
				case <-ctx.Done():
					return
				default:
					continue
				}
			}
			if !errors.Is(err, net.ErrClosed) {
				log.Printf("read failed: %+v", err)
			}
			stop() // fatal or closed: tear the whole pipeline down
			return
		}

		data := buf[:n]
		if n < 5 || string(data[0:4]) != replyPrologue {
			log.Printf("unexpected packet from %s: %q", src, data)
			continue
		}

		for body := data[5:]; len(body) >= 8; body = body[8:] {
			idx := int32(binary.LittleEndian.Uint32(body[0:4]))
			val := math.Float32frombits(binary.LittleEndian.Uint32(body[4:8]))
			select {
			case updates <- Update{idx, val}:
			case <-ctx.Done():
				return // don't block on send during shutdown
			}
		}
	}
}

func makeRREF(freq, index int32, dataref string) []byte {
	pkt := make([]byte, requestBytes)
	copy(pkt[0:], "RREF\x00")
	binary.LittleEndian.PutUint32(pkt[5:], uint32(freq))
	binary.LittleEndian.PutUint32(pkt[9:], uint32(index))
	copy(pkt[13:], dataref)
	return pkt
}
