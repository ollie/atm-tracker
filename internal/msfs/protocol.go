package msfs

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
)

const (
	protocolKittyHawk = 5

	requestOpen                   = 0x01
	requestMapClientEventToSim    = 0x04
	requestTransmitClientEvent    = 0x05
	requestAddToDataDefinition    = 0x0c
	requestDataOnSimObject        = 0x0e
	requestSubscribeToSystemEvent = 0x17
	requestSystemState            = 0x35

	recvException     = 1
	recvOpen          = 2
	recvQuit          = 3
	recvEvent         = 4
	recvSimObjectData = 8
	recvSystemState   = 15

	dataTypeFloat64   = 4
	periodSecond      = 4
	objectUser        = 0
	unused            = 0xffffffff
	priorityHighest   = 1
	flagGroupPriority = 0x10

	headerSize     = 12
	maxMessageSize = 1 << 20
)

type message struct {
	kind    uint32
	payload []byte
}

type packet struct {
	buf []byte
}

func newPacket(request uint32) *packet {
	p := &packet{buf: make([]byte, 0, 320)}
	p.u32(0)
	p.u32(protocolKittyHawk)
	p.u32(0xf0000000 | request)
	p.u32(0)
	return p
}

func (p *packet) u32(v uint32) *packet {
	p.buf = binary.LittleEndian.AppendUint32(p.buf, v)
	return p
}

func (p *packet) f32(v float32) *packet {
	return p.u32(math.Float32bits(v))
}

func (p *packet) str(s string, size int) *packet {
	b := make([]byte, size)
	copy(b, s)
	p.buf = append(p.buf, b...)
	return p
}

func (p *packet) bytes(sendID uint32) []byte {
	binary.LittleEndian.PutUint32(p.buf[0:4], uint32(len(p.buf)))
	binary.LittleEndian.PutUint32(p.buf[12:16], sendID)
	return p.buf
}

func openPacket(appName string) *packet {
	return newPacket(requestOpen).
		str(appName, 256).
		u32(0).
		str("\x00HK", 4).
		u32(11).u32(0).u32(62651).u32(3)
}

func addToDataDefinition(definition uint32, name, units string) *packet {
	return newPacket(requestAddToDataDefinition).
		u32(definition).
		str(name, 256).
		str(units, 256).
		u32(dataTypeFloat64).
		f32(0).
		u32(unused)
}

func requestDataEverySecond(request, definition uint32) *packet {
	return newPacket(requestDataOnSimObject).
		u32(request).
		u32(definition).
		u32(objectUser).
		u32(periodSecond).
		u32(0).u32(0).u32(0).u32(0)
}

func subscribeToSystemEvent(event uint32, name string) *packet {
	return newPacket(requestSubscribeToSystemEvent).u32(event).str(name, 256)
}

func systemStateRequest(request uint32, name string) *packet {
	return newPacket(requestSystemState).u32(request).str(name, 256)
}

func mapClientEventToSimEvent(event uint32, name string) *packet {
	return newPacket(requestMapClientEventToSim).u32(event).str(name, 256)
}

func transmitClientEvent(event uint32) *packet {
	return newPacket(requestTransmitClientEvent).
		u32(objectUser).
		u32(event).
		u32(0).
		u32(priorityHighest).
		u32(flagGroupPriority)
}

func readMessage(r io.Reader) (message, error) {
	var header [headerSize]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return message{}, err
	}

	size := binary.LittleEndian.Uint32(header[0:4])
	if size < headerSize || size > maxMessageSize {
		return message{}, fmt.Errorf("message of %d bytes", size)
	}

	payload := make([]byte, size-headerSize)
	if _, err := io.ReadFull(r, payload); err != nil {
		return message{}, err
	}

	return message{kind: binary.LittleEndian.Uint32(header[8:12]), payload: payload}, nil
}

func decodeEvent(payload []byte) (event, data uint32, err error) {
	if len(payload) < 12 {
		return 0, 0, errors.New("short event")
	}

	return binary.LittleEndian.Uint32(payload[4:8]), binary.LittleEndian.Uint32(payload[8:12]), nil
}

func decodeSystemState(payload []byte) (request, value uint32, err error) {
	if len(payload) < 8 {
		return 0, 0, errors.New("short system state")
	}

	return binary.LittleEndian.Uint32(payload[0:4]), binary.LittleEndian.Uint32(payload[4:8]), nil
}

func decodeSimObjectData(payload []byte, request uint32, count int) ([]float64, error) {
	if len(payload) < 28 {
		return nil, errors.New("short data block")
	}
	if got := binary.LittleEndian.Uint32(payload[0:4]); got != request {
		return nil, fmt.Errorf("data block for request %d, asked as %d", got, request)
	}

	data := payload[28:]
	if len(data) != 8*count {
		return nil, fmt.Errorf("data block of %d bytes, %d variables need %d", len(data), count, 8*count)
	}

	values := make([]float64, count)
	for i := range values {
		values[i] = math.Float64frombits(binary.LittleEndian.Uint64(data[8*i:]))
	}

	return values, nil
}

func decodeOpen(payload []byte) string {
	if len(payload) < 288 {
		return "short open reply"
	}

	v := func(at int) uint32 { return binary.LittleEndian.Uint32(payload[at:]) }
	return fmt.Sprintf("%s %d.%d build %d.%d, SimConnect %d.%d build %d.%d",
		cstring(payload[:256]), v(256), v(260), v(264), v(268), v(272), v(276), v(280), v(284))
}

func cstring(b []byte) string {
	if end := bytes.IndexByte(b, 0); end >= 0 {
		b = b[:end]
	}

	return string(b)
}

func decodeException(payload []byte) string {
	if len(payload) < 12 {
		return "short exception"
	}

	return fmt.Sprintf("exception %d for send ID %d, item %d",
		binary.LittleEndian.Uint32(payload[0:4]),
		binary.LittleEndian.Uint32(payload[4:8]),
		binary.LittleEndian.Uint32(payload[8:12]))
}
