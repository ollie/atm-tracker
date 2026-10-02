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

	pauseOn     = "sim/operation/pause_on"
	pauseToggle = "sim/operation/pause_toggle"
	pausedIndex = 1
	pausedHz    = 10
	PauseSettle = time.Second
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

func Pause(addr string, settle time.Duration) error {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", addr, err)
	}

	conn, err := net.ListenUDP("udp", nil)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err = conn.WriteToUDP(makeRREF(pausedHz, pausedIndex, pausedDataref), udpAddr); err != nil {
		return fmt.Errorf("subscribe %s: %w", pausedDataref, err)
	}
	defer func() { _, _ = conn.WriteToUDP(makeRREF(0, pausedIndex, pausedDataref), udpAddr) }()

	if _, err = conn.WriteToUDP(makeCMND(pauseOn), udpAddr); err != nil {
		return fmt.Errorf("%s: %w", pauseOn, err)
	}

	paused, err := awaitPaused(conn, settle)
	if err != nil || paused {
		return err
	}

	if _, err = conn.WriteToUDP(makeCMND(pauseToggle), udpAddr); err != nil {
		return fmt.Errorf("%s: %w", pauseToggle, err)
	}

	return nil
}

func awaitPaused(conn *net.UDPConn, settle time.Duration) (bool, error) {
	buf := make([]byte, 2048)
	zeros := int(settle / (time.Second / pausedHz))
	answered := false

	for {
		_ = conn.SetReadDeadline(time.Now().Add(settle))
		n, _, err := conn.ReadFromUDP(buf)
		if errors.Is(err, os.ErrDeadlineExceeded) {
			if !answered {
				return false, errors.New("no answer from X-Plane, is it running?")
			}
			return false, errors.New("X-Plane went quiet before it paused")
		}
		if err != nil {
			return false, fmt.Errorf("read: %w", err)
		}

		updates, ok := decodeRREF(buf[:n])
		if !ok {
			continue
		}
		for _, u := range updates {
			if u.Index != pausedIndex {
				continue
			}
			answered = true
			if u.Value == 1 {
				return true, nil
			}
			if zeros--; zeros <= 0 {
				return false, nil
			}
		}
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

		decoded, ok := decodeRREF(buf[:n])
		if !ok {
			log.Printf("unexpected packet from %s: %q", src, buf[:n])
			continue
		}

		for _, u := range decoded {
			select {
			case updates <- u:
			case <-ctx.Done():
				return // don't block on send during shutdown
			}
		}
	}
}

func decodeRREF(data []byte) ([]Update, bool) {
	if len(data) < 5 || string(data[0:4]) != replyPrologue {
		return nil, false
	}

	body := data[5:]
	updates := make([]Update, 0, len(body)/8)
	for ; len(body) >= 8; body = body[8:] {
		idx := int32(binary.LittleEndian.Uint32(body[0:4]))
		val := math.Float32frombits(binary.LittleEndian.Uint32(body[4:8]))
		updates = append(updates, Update{idx, val})
	}

	return updates, true
}

func makeCMND(command string) []byte {
	return append([]byte("CMND\x00"), command...)
}

func makeRREF(freq, index int32, dataref string) []byte {
	pkt := make([]byte, requestBytes)
	copy(pkt[0:], "RREF\x00")
	binary.LittleEndian.PutUint32(pkt[5:], uint32(freq))
	binary.LittleEndian.PutUint32(pkt[9:], uint32(index))
	copy(pkt[13:], dataref)
	return pkt
}
