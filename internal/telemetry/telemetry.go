// Package telemetry is one sample of what the aircraft is doing.
package telemetry

import (
	"math"
	"time"
)

const (
	MaxEngines = 8

	meterToFeetCoef = 3.28084
	mpsToKtsCoef    = 1.94384
)

type Kts int

type Position struct {
	UUID                string    `json:"uuid"`
	ReportedAt          time.Time `json:"reportedAt"`
	Paused              bool      `json:"paused"`
	OnGround            bool      `json:"onGround"`
	Lat                 float32   `json:"lat"`
	Lon                 float32   `json:"lon"`
	MagneticHeading     int       `json:"magneticHeading"`
	TrueHeading         int       `json:"trueHeading"`
	Track               int       `json:"track"`
	AltitudeFt          int       `json:"altitudeFt"`
	HeightAglFt         int       `json:"heightAglFt"`
	GroundspeedKt       Kts       `json:"groundspeedKt"`
	IndicatedAirspeedKt Kts       `json:"indicatedAirspeedKt"`
	TrueAirspeedKt      Kts       `json:"trueAirspeedKt"`
	Mach                float32   `json:"mach"`
	PayloadKg           float32   `json:"payloadKg"`
	FuelKg              float32   `json:"fuelKg"`
	FlapRatio           float32   `json:"flapRatio"`
	NumEngines          int       `json:"numEngines"`
	EnginesRunning      int       `json:"enginesRunning"`

	IndicatedAirspeed2 Kts `json:"-"`

	engineBurning [MaxEngines]bool
}

func (p *Position) SetEngineBurning(idx int, value float32) {
	p.engineBurning[idx] = value == 1

	running := 0
	for _, burning := range p.engineBurning {
		if burning {
			running++
		}
	}
	p.EnginesRunning = running
}

func RoundToInt(value float32) int {
	return int(math.Round(float64(value)))
}

func MetersToFeet(value float32) int {
	return RoundToInt(value * meterToFeetCoef)
}

func MpsToKts(value float32) Kts {
	return Kts(RoundToInt(value * mpsToKtsCoef))
}
