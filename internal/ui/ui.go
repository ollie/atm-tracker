// Package ui is the tracker window: what the flight is doing and what went wrong.
package ui

import (
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
	"github.com/ollie/atm-tracker/internal/api"
	"github.com/skratchdot/open-golang/open"
)

const (
	zuluLayout  = "15:04:05Z"
	buttonWidth = 150
	buttonHigh  = 40
	windowWidth = 800
	windowHigh  = 600
	lbPerKg     = 2.20462262185
	unitKg      = "kg"
	unitLb      = "lb"

	prefAlwaysOnTop = "alwaysOnTop"
)

var warningKinds = map[string]string{
	"not_at_departure": "The aircraft is not at the departure airport",
	"diverted":         "Landed away from the filed arrival",
	"fuel_anomaly":     "The fuel readings do not add up, the flight was rejected",
}

type Actions struct {
	SignIn   func()
	SignOut  func()
	Quit     func()
	PauseSim func()
	Deboard  func()
}

type UI struct {
	Win fyne.Window

	flightValue  *widget.Label
	statusValue  *widget.Label
	targetsValue *widget.Label
	actualValue  *widget.Label
	enginesValue *widget.Label
	simValue     *widget.Label
	eventValue   *widget.Label
	noteValue    *widget.Label

	loginButton   *widget.Button
	logoutButton  *widget.Button
	deboardButton *widget.Button

	logNote   *widget.Label
	logButton *widget.Button
	logPath   string

	onTopCheck *widget.Check
	onTopNote  *widget.Label

	timerHours   *widget.Entry
	timerMinutes *widget.Entry
	timerSeconds *widget.Entry
	timerFields  []*widget.Entry
	timerButton  *widget.Button
	timerLeft    *widget.Label
	timerNote    *widget.Label
	timerEnd     time.Time
	timerDone    chan struct{}
	timerTab     *container.TabItem
	tabs         *container.AppTabs
	pauseSim     func()

	unit    string
	note    string
	warning string
}

func New(app fyne.App, version string, actions Actions) *UI {
	u := &UI{
		Win:          app.NewWindow("Air Transport Magnate Tracker v" + version),
		flightValue:  widget.NewLabel(""),
		statusValue:  widget.NewLabel(""),
		targetsValue: widget.NewLabel(""),
		actualValue:  widget.NewLabel(""),
		enginesValue: widget.NewLabel(""),
		simValue:     widget.NewLabel(""),
		eventValue:   widget.NewLabel(""),
		noteValue:    widget.NewLabel(""),
		logNote:      widget.NewLabel(""),
		onTopNote:    widget.NewLabel("Takes effect on the next start"),
		timerHours:   widget.NewEntry(),
		timerMinutes: widget.NewEntry(),
		timerSeconds: widget.NewEntry(),
		timerLeft:    widget.NewLabel(""),
		timerNote:    widget.NewLabel(""),
		pauseSim:     actions.PauseSim,
		unit:         unitKg,
	}
	u.timerButton = widget.NewButton("Start", u.toggleTimer)
	u.timerFields = []*widget.Entry{u.timerHours, u.timerMinutes, u.timerSeconds}
	for _, e := range u.timerFields {
		e.OnChanged = func(string) { u.allowStart() }
		e.OnSubmitted = func(string) { u.startTimer() }
	}
	u.allowStart()

	prefs := app.Preferences()
	u.onTopNote.Hide()
	u.onTopCheck = widget.NewCheck("Always on top", func(on bool) {
		prefs.SetBool(prefAlwaysOnTop, on)
		if on {
			u.requestAlwaysOnTop()
			u.onTopNote.Hide()
		} else {
			u.onTopNote.Show()
		}
	})
	u.onTopCheck.SetChecked(prefs.Bool(prefAlwaysOnTop))

	u.noteValue.Importance = widget.WarningImportance
	u.logNote.Truncation = fyne.TextTruncateEllipsis
	u.logButton = widget.NewButton("Open log", u.openLog)
	u.logButton.Hide()

	u.loginButton = widget.NewButton("Log in", actions.SignIn)
	u.logoutButton = widget.NewButton("Log out", actions.SignOut)
	u.logoutButton.Hide()
	quitButton := widget.NewButton("Quit", actions.Quit)
	u.deboardButton = widget.NewButton("Deboard", func() {
		u.deboardButton.Disable()
		actions.Deboard()
	})
	u.deboardButton.Disable()

	flightPane := container.NewVBox(
		container.New(layout.NewFormLayout(),
			fieldLabel("Flight"), u.flightValue,
			fieldLabel("Status"), u.statusValue,
			fieldLabel("Targets"), u.targetsValue,
			fieldLabel("Actual"), u.actualValue,
			fieldLabel("Engines"), container.NewHBox(u.enginesValue, u.deboardButton),
			fieldLabel("Sim"), u.simValue,
			fieldLabel("Last event"), u.eventValue,
		),
		u.noteValue,
	)

	logPane := container.NewVBox(
		container.New(layout.NewFormLayout(),
			fieldLabel("Log file"), u.logNote,
		),
		container.NewHBox(u.logButton),
	)

	settingsPane := container.NewVBox(u.onTopCheck, u.onTopNote)

	timerPane := container.NewVBox(
		container.NewHBox(
			u.timerHours, widget.NewLabel("h"), u.timerMinutes, widget.NewLabel("m"), u.timerSeconds, widget.NewLabel("s"),
			u.timerButton, u.timerLeft,
		),
		u.timerNote,
	)

	u.timerTab = container.NewTabItem("Timer", container.NewPadded(timerPane))
	u.tabs = container.NewAppTabs(
		container.NewTabItem("Flight", container.NewPadded(flightPane)),
		container.NewTabItem("Log", container.NewPadded(logPane)),
		container.NewTabItem("Settings", container.NewPadded(settingsPane)),
		u.timerTab,
	)

	buttons := container.NewGridWrap(
		fyne.NewSize(buttonWidth, buttonHigh),
		u.loginButton, u.logoutButton, quitButton,
	)

	u.Win.SetContent(container.NewBorder(nil, nil, buttons, nil, u.tabs))
	u.Win.Resize(fyne.NewSize(windowWidth, windowHigh))
	u.Win.SetCloseIntercept(actions.Quit)

	return u
}

func (u *UI) requestAlwaysOnTop() {
	if w, ok := u.Win.(desktop.Window); ok {
		w.RequestAlwaysOnTop()
	}
}

func fieldLabel(text string) *widget.Label {
	return widget.NewLabelWithStyle(text, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
}

func (u *UI) toggleTimer() {
	if u.timerEnd.IsZero() {
		u.startTimer()
	} else {
		u.stopTimer()
	}
}

func (u *UI) startTimer() {
	left, err := timerDuration(u.timerFields)
	if err != nil || left <= 0 {
		return
	}

	u.timerEnd = time.Now().Add(left).Round(0)
	for _, e := range u.timerFields {
		e.Disable()
	}
	u.timerButton.SetText("Stop")
	u.timerNote.SetText("Pausing the sim at " + u.timerEnd.Local().Format(time.TimeOnly))
	u.showLeft(left)

	done := make(chan struct{})
	u.timerDone = done

	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-done:
				return
			case now := <-ticker.C:
				fyne.Do(func() { u.tick(now) })
			}
		}
	}()
}

func (u *UI) allowStart() {
	if !u.timerEnd.IsZero() {
		return
	}

	if left, err := timerDuration(u.timerFields); err != nil || left <= 0 {
		u.timerButton.Disable()
	} else {
		u.timerButton.Enable()
	}
}

func timerDuration(fields []*widget.Entry) (time.Duration, error) {
	var left time.Duration

	for i, unit := range []time.Duration{time.Hour, time.Minute, time.Second} {
		if fields[i].Text == "" {
			continue
		}

		n, err := strconv.ParseUint(fields[i].Text, 10, 16)
		if err != nil {
			return 0, err
		}

		left += time.Duration(n) * unit
	}

	return left, nil
}

func (u *UI) tick(now time.Time) {
	if u.timerEnd.IsZero() {
		return
	}

	left := u.timerEnd.Sub(now)
	if left > 0 {
		u.showLeft(left)
		return
	}

	u.stopTimer()
	u.showLeft(0)
	u.timerNote.SetText("Time is up, pausing the sim at " + now.Local().Format(time.TimeOnly))
	if u.pauseSim != nil {
		u.pauseSim()
	}
}

func (u *UI) showLeft(left time.Duration) {
	secs := int(math.Ceil(left.Seconds()))
	h, m, sec := secs/3600, secs%3600/60, secs%60
	u.timerHours.SetText(strconv.Itoa(h))
	u.timerMinutes.SetText(strconv.Itoa(m))
	u.timerSeconds.SetText(strconv.Itoa(sec))
	clock := fmt.Sprintf("%02d:%02d:%02d", h, m, sec)
	u.timerLeft.SetText(clock)
	u.setTimerTab("Timer (" + clock + ")")
}

func (u *UI) setTimerTab(text string) {
	u.timerTab.Text = text
	u.tabs.Refresh()
}

func (u *UI) stopTimer() {
	close(u.timerDone)
	u.timerEnd = time.Time{}
	for _, e := range u.timerFields {
		e.Enable()
	}
	u.timerButton.SetText("Start")
	u.timerLeft.SetText("")
	u.timerNote.SetText("")
	u.setTimerTab("Timer")
	u.allowStart()
}

func (u *UI) SigningIn() {
	fyne.Do(func() {
		u.loginButton.Disable()
		u.flightValue.SetText("Waiting for the browser")
		u.resetFlight()
		u.resetNotes()
	})
}

func (u *UI) SignedIn() {
	fyne.Do(func() {
		u.loginButton.Hide()
		u.logoutButton.Show()
		u.logoutButton.Enable()
		u.flightValue.SetText("Looking for your flight")
		u.resetFlight()
		u.resetNotes()
	})
}

func (u *UI) SignedOut() {
	fyne.Do(func() {
		u.logoutButton.Hide()
		u.loginButton.Show()
		u.loginButton.Enable()
		u.flightValue.SetText("Log in to start tracking")
		u.resetFlight()
		u.resetNotes()
	})
}

func (u *UI) SetNoFlight() {
	fyne.Do(func() {
		u.flightValue.SetText("No flight to fly")
		u.resetFlight()
	})
}

func (u *UI) StartFlight(item *api.FlightInfo) {
	u.SetFlight(item)

	fyne.Do(func() {
		u.eventValue.SetText("–")
		u.actualValue.SetText("–")
		u.enginesValue.SetText("–")
		u.resetNotes()
	})
}

func (u *UI) SetFlight(item *api.FlightInfo) {
	route := fmt.Sprintf("%s → %s", item.Departure.Code, item.Arrival.Code)
	unit := unitOf(item.WeightUnit)
	targets := targetsText(item)

	fyne.Do(func() {
		u.unit = unit
		u.flightValue.SetText(route)
		u.showStatus(item.Status)
		u.targetsValue.SetText(targets)
	})
}

func (u *UI) showStatus(status string) {
	u.statusValue.SetText(statusText(status))
	if status == "taxi_to_gate" {
		u.deboardButton.Enable()
	} else {
		u.deboardButton.Disable()
	}
}

func targetsText(item *api.FlightInfo) string {
	return loadText(unitOf(item.WeightUnit), float64(item.TargetPayloadKg), float64(item.TargetFuelKg))
}

func loadText(unit string, payloadKg, fuelKg float64) string {
	return fmt.Sprintf("%d %s payload, %d %s fuel",
		weightIn(unit, payloadKg), unit,
		weightIn(unit, fuelKg), unit)
}

func unitOf(unit string) string {
	if unit == unitLb {
		return unitLb
	}

	return unitKg
}

func weightIn(unit string, kg float64) int {
	if unit == unitLb {
		return int(math.Round(kg * lbPerKg))
	}

	return int(math.Round(kg))
}

func (u *UI) resetFlight() {
	u.statusValue.SetText("–")
	u.targetsValue.SetText("–")
	u.actualValue.SetText("–")
	u.enginesValue.SetText("–")
	u.deboardButton.Disable()
	u.simValue.SetText("Not tracking")
	u.eventValue.SetText("–")
}

func (u *UI) resetNotes() {
	u.note, u.warning = "", ""
	u.renderNote()
}

func (u *UI) SetLive(simName string, live bool) {
	text := "– (waiting for sim)"
	if live {
		text = simName + " (receiving)"
	}

	fyne.Do(func() { u.simValue.SetText(text) })
}

func (u *UI) SetActual(payloadKg, fuelKg float32, enginesRunning int) {
	engines := "Not running"
	if enginesRunning > 0 {
		engines = "Running"
	}

	fyne.Do(func() {
		u.actualValue.SetText(loadText(u.unit, float64(payloadKg), float64(fuelKg)))
		u.enginesValue.SetText(engines)
	})
}

func (u *UI) SetNote(note string) {
	fyne.Do(func() {
		u.note = note
		u.renderNote()
	})
}

func (u *UI) renderNote() {
	text := u.warning
	if text == "" {
		text = u.note
	}

	u.noteValue.SetText(text)
}

func (u *UI) SetProgress(result *api.PositionsResult) {
	event, warning := "", ""

	for _, item := range result.Events {
		event = fmt.Sprintf("%s at %s", statusText(item.Kind), item.OccurredAt.UTC().Format(zuluLayout))
		if text, ok := warningKinds[item.Kind]; ok {
			warning = text
		}
	}

	fyne.Do(func() {
		u.showStatus(result.Status)
		if event != "" {
			u.eventValue.SetText(event)
		}

		u.note = ""
		if warning != "" {
			u.warning = warning
		}
		u.renderNote()
	})
}

func statusText(status string) string {
	return strings.ReplaceAll(status, "_", " ")
}

func (u *UI) SetLogFile(path string) {
	fyne.Do(func() {
		u.logPath = path

		if path == "" {
			u.logNote.SetText("Could not open the log file, the log is on the terminal only")
			u.logButton.Hide()
			return
		}

		u.logNote.SetText(path)
		u.logButton.Show()
	})
}

func (u *UI) openLog() {
	if u.logPath == "" {
		return
	}

	if err := open.Run(u.logPath); err != nil {
		log.Printf("could not open %s: %v", u.logPath, err)
	}
}
