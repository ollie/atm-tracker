package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"io"
	"log"
	"math"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/google/uuid"
	"resty.dev/v3"
)

const appID = "cz.oldrichvetesnik.atmtracker"

type positionData struct {
	UUID               string    `json:"uuid"`
	Timestamp          time.Time `json:"timestamp"`
	Paused             bool      `json:"paused"`
	Latitude           float32   `json:"latitude"`
	Longitude          float32   `json:"longitude"`
	MagneticHeading    int       `json:"magneticHeading"`
	TrueHeading        int       `json:"trueHeading"`
	Track              int       `json:"track"`
	ActualAltitude     int       `json:"actualAltitude"`
	HeightAGL          int       `json:"heightAGL"`
	Groundspeed        kts       `json:"groundspeed"`
	IndicatedAirspeed  kts       `json:"indicatedAirspeed"`
	IndicatedAirspeed2 kts       `json:"indicatedAirspeed2"`
	TrueAirspeed       kts       `json:"trueAirspeed"`
	MachAirspeed       float32   `json:"machAirspeed"`
	PayloadKg          float32   `json:"payload_kg"`
	FuelKg             float32   `json:"fuel_kg"`
}

type datarefMapItem struct {
	name   string
	assign func(*positionData, float32)
}

type datarefTickUpdate struct {
	idx int32
	val float32
}

type kts int

const (
	windowSize      = 100
	snapshotEvery   = 2 * time.Second
	sendEvery       = 10 * time.Second
	readPoll        = 1 * time.Second
	cleanupTimeout  = 1 * time.Second
	shutdownGrace   = 5 * time.Second
	meterToFeetCoef = 3.28084
	mpsToKtsCoef    = 1.94384

	xplaneAddr = "127.0.0.1:49000"
)

// https://developer.x-plane.com/datarefs/
var datarefsMap = map[int32]datarefMapItem{
	1: {
		name:   "sim/time/paused",
		assign: func(p *positionData, value float32) { p.Paused = value == 1 },
	},
	2: {
		name:   "sim/flightmodel/position/latitude",
		assign: func(p *positionData, value float32) { p.Latitude = value },
	},
	3: {
		name:   "sim/flightmodel/position/longitude",
		assign: func(p *positionData, value float32) { p.Longitude = value },
	},
	4: {
		name:   "sim/flightmodel/position/mag_psi", // magnetic heading deg
		assign: func(p *positionData, value float32) { p.MagneticHeading = roundToInt(value) },
	},
	5: {
		name:   "sim/flightmodel/position/true_psi", // true heading deg
		assign: func(p *positionData, value float32) { p.TrueHeading = roundToInt(value) },
	},
	6: {
		name:   "sim/flightmodel/position/hpath", // track deg
		assign: func(p *positionData, value float32) { p.Track = roundToInt(value) },
	},
	7: {
		name:   "sim/flightmodel/position/elevation", // meters MSL
		assign: func(p *positionData, value float32) { p.ActualAltitude = metersToFeet(value) },
	},
	8: {
		name:   "sim/flightmodel/position/y_agl", // height above ground in m
		assign: func(p *positionData, value float32) { p.HeightAGL = metersToFeet(value) },
	},
	9: {
		name:   "sim/flightmodel/position/groundspeed", // m/s
		assign: func(p *positionData, value float32) { p.Groundspeed = mpsToKts(value) },
	},
	10: {
		name:   "sim/flightmodel/position/indicated_airspeed", // kias
		assign: func(p *positionData, value float32) { p.IndicatedAirspeed = mpsToKts(value) },
	},
	11: {
		name:   "sim/flightmodel/position/indicated_airspeed2", // kias
		assign: func(p *positionData, value float32) { p.IndicatedAirspeed2 = mpsToKts(value) },
	},
	12: {
		name:   "sim/flightmodel/position/true_airspeed", // m/s
		assign: func(p *positionData, value float32) { p.TrueAirspeed = mpsToKts(value) },
	},
	13: {
		name:   "sim/flightmodel/misc/machno", // mach number
		assign: func(p *positionData, value float32) { p.MachAirspeed = value },
	},
	14: {
		name:   "sim/flightmodel/weight/m_fixed", // XP11, XP12 too? With stations? Need to test
		assign: func(p *positionData, value float32) { p.PayloadKg = value },
	},
	15: {
		name:   "sim/flightmodel/weight/m_fuel_total", // XP11, XP12 too? Need to test
		assign: func(p *positionData, value float32) { p.FuelKg = value },
	},
}

type uiLogWriter struct {
	text   *widget.Label
	scroll *container.Scroll
}

func (w *uiLogWriter) Write(p []byte) (int, error) {
	line := string(p)
	fyne.Do(func() { // on UI thread, doesn't block logger
		lines := append(strings.Split(w.text.Text, "\n"), strings.TrimRight(line, "\n"))
		if len(lines) > 500 {
			lines = lines[len(lines)-500:]
		}
		w.text.SetText(strings.Join(lines, "\n"))
		w.scroll.ScrollToBottom()
	})

	return len(p), nil
}

const version = "0.0.1"

var (
	apiBaseURL        = "http://localhost:4567"
	apiUserAgent      = "ATM Tracker v" + version
	apiRequestTimeout = 5 * time.Second
)

func main() {
	// Ctrl+C, SIGTERM quits the app
	appCtx, appStop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer appStop()

	// Manual stop button
	var trackerStop context.CancelFunc

	var wg sync.WaitGroup

	app := app.NewWithID(appID)
	win := app.NewWindow("Air Transport Magnate Tracker")
	top := container.NewPadded(canvas.NewText("Air Transport Magnate", color.White))
	var startButton, stopButton *widget.Button

	startButton = widget.NewButton("Start", func() {
		var trackerCtx context.Context
		trackerCtx, trackerStop = context.WithCancel(context.Background())
		log.Println("starting")
		if err := startTracker(trackerCtx, trackerStop, &wg); err != nil {
			log.Printf("start failed: %+v", err)
			trackerStop()
			return
		}
		stopButton.Enable()
		stopButton.Show()
		startButton.Disable()
		startButton.Hide()
	})

	stopButton = widget.NewButton("Stop", func() {
		log.Println("stopping")
		if trackerStop != nil {
			trackerStop() // Cancel this run
		}
		startButton.Enable()
		startButton.Show()
		stopButton.Disable()
		stopButton.Hide()
	})

	stopButton.Disable()
	stopButton.Hide()

	shutdown := func() {
		log.SetOutput(os.Stderr)
		if trackerStop != nil {
			trackerStop()
		}
		app.Quit()
	}

	quitButton := widget.NewButton("Quit", func() {
		log.Println("quitting")
		shutdown()
	})

	win.SetCloseIntercept(shutdown)

	logText := widget.NewLabel("")
	logText.Wrapping = fyne.TextWrapWord
	middle := container.NewVScroll(logText)

	log.SetOutput(io.MultiWriter(os.Stderr, &uiLogWriter{text: logText, scroll: middle}))

	left := container.NewGridWrap(fyne.NewSize(150, 40), startButton, stopButton, quitButton)
	content := container.NewBorder(top, nil, left, nil, middle)
	win.SetContent(content)

	go func() {
		<-appCtx.Done()
		fyne.Do(shutdown)
	}()

	win.Resize(fyne.NewSize(800, 600))
	win.ShowAndRun()

	if !waitFor(&wg, shutdownGrace) {
		log.Printf("gave up after %s waiting for tracker goroutines", shutdownGrace)
	}
	log.Println("stopped")
}

func waitFor(wg *sync.WaitGroup, timeout time.Duration) bool {
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

func startTracker(ctx context.Context, stop context.CancelFunc, wg *sync.WaitGroup) error {
	client := resty.New().
		SetBaseURL(apiBaseURL).
		SetHeader("User-Agent", apiUserAgent).
		SetHeader("Content-Type", "application/json").
		SetTimeout(apiRequestTimeout)

	addr, err := net.ResolveUDPAddr("udp", xplaneAddr)
	if err != nil {
		_ = client.Close()
		return fmt.Errorf("resolve %s: %w", xplaneAddr, err)
	}

	conn, err := net.ListenUDP("udp", nil) // nil laddr means 0.0.0.0
	if err != nil {
		_ = client.Close()
		return fmt.Errorf("listen: %w", err)
	}

	// Subscribe at 1 Hz. Fail before launching anything if this doesn't work.
	for idx, dataref := range datarefsMap {
		if _, err := conn.WriteToUDP(makeRREF(1, idx, dataref.name), addr); err != nil {
			_ = conn.Close()
			_ = client.Close()
			return fmt.Errorf("subscribe %s: %w", dataref.name, err)
		}
	}

	updates := make(chan datarefTickUpdate, 64)
	snapshots := make(chan positionData, windowSize)

	wg.Add(4)
	go func() { defer wg.Done(); runReader(ctx, conn, updates, stop) }()
	go func() { defer wg.Done(); runCoordinator(ctx, updates, snapshots) }()
	go func() { defer wg.Done(); runSender(ctx, client, snapshots) }()
	go func() { defer wg.Done(); cleanupTracker(ctx, addr, conn) }()

	return nil
}

func runReader(ctx context.Context, conn *net.UDPConn, updates chan<- datarefTickUpdate, stop context.CancelFunc) {
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
		if n < 5 || string(data[0:4]) != "RREF" {
			log.Printf("unexpected packet from %s: %q", src, data)
			continue
		}

		for body := data[5:]; len(body) >= 8; body = body[8:] {
			idx := int32(binary.LittleEndian.Uint32(body[0:4]))
			val := math.Float32frombits(binary.LittleEndian.Uint32(body[4:8]))
			select {
			case updates <- datarefTickUpdate{idx, val}:
			case <-ctx.Done():
				return // don't block on send during shutdown
			}
		}
	}
}

func runCoordinator(ctx context.Context, updates <-chan datarefTickUpdate, snapshots chan<- positionData) {
	snapshotTicker := time.NewTicker(snapshotEvery)
	defer snapshotTicker.Stop()

	var posData positionData

	for {
		select {
		case <-ctx.Done():
			return

		case u := <-updates:
			item, ok := datarefsMap[u.idx]
			if !ok {
				log.Printf("unknown dataref at index %d", u.idx)
				continue
			}
			item.assign(&posData, u.val)

		case <-snapshotTicker.C:
			if posData.Paused {
				log.Printf("Skipping paused: %+v", posData)
				continue
			}
			posData.UUID = uuid.NewString()
			posData.Timestamp = time.Now()
			log.Printf("Collected: %+v", posData)
			select {
			case snapshots <- posData:
			case <-ctx.Done():
				return
			}
		}
	}
}

func runSender(ctx context.Context, client *resty.Client, snapshots <-chan positionData) {
	defer client.Close()

	pending := make([]positionData, 0, windowSize)
	sendTicker := time.NewTicker(sendEvery)
	defer sendTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case p := <-snapshots:
			pending = append(pending, p)
			if len(pending) > windowSize {
				pending = pending[1:] // cap: drop oldest unsent
			}

		case <-sendTicker.C:
			if len(pending) == 0 {
				continue
			}
			if err := sendPositions(ctx, client, pending); err != nil {
				log.Printf("send failed, keeping %d positions for retry: %v", len(pending), err)
				continue // leave pending intact - retry next tick
			}
			log.Printf("sent %d positions", len(pending))
			pending = pending[:0] // success: clear
		}
	}
}

func cleanupTracker(ctx context.Context, addr *net.UDPAddr, conn *net.UDPConn) {
	<-ctx.Done()
	_ = conn.SetWriteDeadline(time.Now().Add(cleanupTimeout))
	for idx, dataref := range datarefsMap {
		conn.WriteToUDP(makeRREF(0, idx, dataref.name), addr)
	}
	_ = conn.Close() // unblocks runReader (ReadFromUDP → net.ErrClosed)
}

func sendPositions(ctx context.Context, client *resty.Client, batch []positionData) error {
	body, err := json.Marshal(batch)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	res, err := client.R().
		SetContext(ctx). // abort on shutdown
		SetBody(body).
		Post("/positions")
	if err != nil {
		return fmt.Errorf("request: %w", err) // timeout, connection refused, DNS
	}
	if res.IsStatusFailure() {
		return fmt.Errorf("server returned %s", res.Status()) // 4xx, 5xx
	}
	return nil
}

func makeRREF(freq, index int32, dataref string) []byte {
	pkt := make([]byte, 413)
	copy(pkt[0:], "RREF\x00")
	binary.LittleEndian.PutUint32(pkt[5:], uint32(freq))
	binary.LittleEndian.PutUint32(pkt[9:], uint32(index))
	copy(pkt[13:], dataref)
	return pkt
}

func roundToInt(value float32) int {
	return int(math.Round(float64(value)))
}

func metersToFeet(value float32) int {
	return roundToInt(value * meterToFeetCoef)
}

func mpsToKts(value float32) kts {
	return kts(roundToInt(value * mpsToKtsCoef))
}
