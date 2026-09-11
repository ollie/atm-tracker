// Package ui is the tracker window: what the flight is doing and what went wrong.
package ui

import (
	"fmt"
	"io"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
	"github.com/ollie/atm-tracker/internal/api"
)

const (
	logLines    = 500
	zuluLayout  = "15:04:05Z"
	buttonWidth = 150
	buttonHigh  = 40
	windowWidth = 800
	windowHigh  = 600
)

var warningKinds = map[string]string{
	"not_at_departure": "the aircraft is not at the departure airport",
	"diverted":         "landed away from the filed arrival",
	"fuel_anomaly":     "the fuel readings do not add up, the flight was rejected",
}

type Actions struct {
	SignIn  func()
	SignOut func()
	Quit    func()
}

type UI struct {
	Win fyne.Window

	flightValue  *widget.Label
	statusValue  *widget.Label
	targetsValue *widget.Label
	simValue     *widget.Label
	eventValue   *widget.Label
	noteValue    *widget.Label

	loginButton  *widget.Button
	logoutButton *widget.Button

	logText   *widget.Label
	logScroll *container.Scroll

	note    string
	warning string
}

func New(app fyne.App, actions Actions) *UI {
	u := &UI{
		Win:          app.NewWindow("Air Transport Magnate Tracker"),
		flightValue:  widget.NewLabel(""),
		statusValue:  widget.NewLabel(""),
		targetsValue: widget.NewLabel(""),
		simValue:     widget.NewLabel(""),
		eventValue:   widget.NewLabel(""),
		noteValue:    widget.NewLabel(""),
		logText:      widget.NewLabel(""),
	}

	u.noteValue.Importance = widget.WarningImportance
	u.logText.Wrapping = fyne.TextWrapWord
	u.logScroll = container.NewVScroll(u.logText)

	u.loginButton = widget.NewButton("Log in", actions.SignIn)
	u.logoutButton = widget.NewButton("Log out", actions.SignOut)
	u.logoutButton.Hide()
	quitButton := widget.NewButton("Quit", actions.Quit)

	flightPane := container.NewVBox(
		container.New(layout.NewFormLayout(),
			fieldLabel("Flight"), u.flightValue,
			fieldLabel("Status"), u.statusValue,
			fieldLabel("Targets"), u.targetsValue,
			fieldLabel("Sim"), u.simValue,
			fieldLabel("Last event"), u.eventValue,
		),
		u.noteValue,
	)

	tabs := container.NewAppTabs(
		container.NewTabItem("Flight", container.NewPadded(flightPane)),
		container.NewTabItem("Log", u.logScroll),
	)

	buttons := container.NewGridWrap(
		fyne.NewSize(buttonWidth, buttonHigh),
		u.loginButton, u.logoutButton, quitButton,
	)

	u.Win.SetContent(container.NewBorder(nil, nil, buttons, nil, tabs))
	u.Win.Resize(fyne.NewSize(windowWidth, windowHigh))
	u.Win.SetCloseIntercept(actions.Quit)

	return u
}

func fieldLabel(text string) *widget.Label {
	return widget.NewLabelWithStyle(text, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
}

func (u *UI) SigningIn() {
	fyne.Do(func() {
		u.loginButton.Disable()
		u.flightValue.SetText("waiting for the browser")
		u.resetFlight()
		u.resetNotes()
	})
}

func (u *UI) SignedIn() {
	fyne.Do(func() {
		u.loginButton.Hide()
		u.logoutButton.Show()
		u.logoutButton.Enable()
		u.flightValue.SetText("looking for your flight")
		u.resetFlight()
		u.resetNotes()
	})
}

func (u *UI) SignedOut() {
	fyne.Do(func() {
		u.logoutButton.Hide()
		u.loginButton.Show()
		u.loginButton.Enable()
		u.flightValue.SetText("log in to start tracking")
		u.resetFlight()
		u.resetNotes()
	})
}

func (u *UI) SetNoFlight() {
	fyne.Do(func() {
		u.flightValue.SetText("no flight to fly")
		u.resetFlight()
	})
}

func (u *UI) StartFlight(item *api.FlightInfo) {
	u.SetFlight(item)

	fyne.Do(func() {
		u.eventValue.SetText("—")
		u.resetNotes()
	})
}

func (u *UI) SetFlight(item *api.FlightInfo) {
	route := fmt.Sprintf("%s → %s", item.Departure.Ident, item.Arrival.Ident)
	targets := fmt.Sprintf("%d kg payload, %d kg fuel", item.TargetPayloadKg, item.TargetFuelKg)

	fyne.Do(func() {
		u.flightValue.SetText(route)
		u.statusValue.SetText(statusText(item.Status))
		u.targetsValue.SetText(targets)
	})
}

func (u *UI) resetFlight() {
	u.statusValue.SetText("—")
	u.targetsValue.SetText("—")
	u.simValue.SetText("not tracking")
	u.eventValue.SetText("—")
}

func (u *UI) resetNotes() {
	u.note, u.warning = "", ""
	u.renderNote()
}

func (u *UI) SetLive(live bool) {
	text := "waiting for X-Plane"
	if live {
		text = "receiving"
	}

	fyne.Do(func() { u.simValue.SetText(text) })
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
	status := statusText(result.Status)
	event, warning := "", ""

	for _, item := range result.Events {
		event = fmt.Sprintf("%s at %s", statusText(item.Kind), item.OccurredAt.UTC().Format(zuluLayout))
		if text, ok := warningKinds[item.Kind]; ok {
			warning = text
		}
	}

	fyne.Do(func() {
		u.statusValue.SetText(status)
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

func (u *UI) LogWriter() io.Writer {
	return &logWriter{ui: u}
}

type logWriter struct {
	ui *UI
}

func (w *logWriter) Write(p []byte) (int, error) {
	line := string(p)
	fyne.Do(func() { // on UI thread, doesn't block logger
		lines := append(strings.Split(w.ui.logText.Text, "\n"), strings.TrimRight(line, "\n"))
		if len(lines) > logLines {
			lines = lines[len(lines)-logLines:]
		}
		w.ui.logText.SetText(strings.Join(lines, "\n"))
		w.ui.logScroll.ScrollToBottom()
	})

	return len(p), nil
}
