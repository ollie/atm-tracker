// Package xplane speaks the sim's RREF protocol and turns what it hears into samples.
package xplane

import (
	"fmt"

	"github.com/ollie/atm-tracker/internal/telemetry"
)

const (
	engineDatarefAt = 19

	Addr = "127.0.0.1:49000"
)

type datarefMapItem struct {
	name   string
	assign func(*telemetry.Position, float32)
}

type Update struct {
	Index int32
	Value float32
}

// https://developer.x-plane.com/datarefs/
var datarefsMap = map[int32]datarefMapItem{
	1: {
		name:   "sim/time/paused",
		assign: func(p *telemetry.Position, value float32) { p.Paused = value == 1 },
	},
	2: {
		name:   "sim/flightmodel/position/latitude",
		assign: func(p *telemetry.Position, value float32) { p.Lat = value },
	},
	3: {
		name:   "sim/flightmodel/position/longitude",
		assign: func(p *telemetry.Position, value float32) { p.Lon = value },
	},
	4: {
		name:   "sim/flightmodel/position/mag_psi", // magnetic heading deg
		assign: func(p *telemetry.Position, value float32) { p.MagneticHeading = telemetry.RoundToInt(value) },
	},
	5: {
		name:   "sim/flightmodel/position/true_psi", // true heading deg
		assign: func(p *telemetry.Position, value float32) { p.TrueHeading = telemetry.RoundToInt(value) },
	},
	6: {
		name:   "sim/flightmodel/position/hpath", // track deg
		assign: func(p *telemetry.Position, value float32) { p.Track = telemetry.RoundToInt(value) },
	},
	7: {
		name:   "sim/flightmodel/position/elevation", // meters MSL
		assign: func(p *telemetry.Position, value float32) { p.AltitudeFt = telemetry.MetersToFeet(value) },
	},
	8: {
		name:   "sim/flightmodel/position/y_agl", // height above ground in m
		assign: func(p *telemetry.Position, value float32) { p.HeightAglFt = telemetry.MetersToFeet(value) },
	},
	9: {
		name:   "sim/flightmodel/position/groundspeed", // m/s
		assign: func(p *telemetry.Position, value float32) { p.GroundspeedKt = telemetry.MpsToKts(value) },
	},
	10: {
		name:   "sim/flightmodel/position/indicated_airspeed", // kias
		assign: func(p *telemetry.Position, value float32) { p.IndicatedAirspeedKt = telemetry.MpsToKts(value) },
	},
	11: {
		name:   "sim/flightmodel/position/indicated_airspeed2", // kias
		assign: func(p *telemetry.Position, value float32) { p.IndicatedAirspeed2 = telemetry.MpsToKts(value) },
	},
	12: {
		name:   "sim/flightmodel/position/true_airspeed", // m/s
		assign: func(p *telemetry.Position, value float32) { p.TrueAirspeedKt = telemetry.MpsToKts(value) },
	},
	13: {
		name:   "sim/flightmodel/misc/machno", // mach number
		assign: func(p *telemetry.Position, value float32) { p.Mach = value },
	},
	14: {
		name:   "sim/flightmodel/weight/m_fixed", // XP11, XP12 too? With stations? Need to test
		assign: func(p *telemetry.Position, value float32) { p.PayloadKg = value },
	},
	15: {
		name:   "sim/flightmodel/weight/m_fuel_total", // XP11, XP12 too? Need to test
		assign: func(p *telemetry.Position, value float32) { p.FuelKg = value },
	},
	16: {
		name:   "sim/flightmodel/failures/onground_any",
		assign: func(p *telemetry.Position, value float32) { p.OnGround = value == 1 },
	},
	17: {
		name:   "sim/cockpit2/controls/flap_ratio",
		assign: func(p *telemetry.Position, value float32) { p.FlapRatio = value },
	},
	18: {
		name:   "sim/aircraft/engine/acf_num_engines",
		assign: func(p *telemetry.Position, value float32) { p.NumEngines = telemetry.RoundToInt(value) },
	},
}

func init() {
	for i := range telemetry.MaxEngines {
		datarefsMap[engineDatarefAt+int32(i)] = datarefMapItem{
			name:   fmt.Sprintf("sim/flightmodel2/engines/engine_is_burning_fuel[%d]", i),
			assign: func(p *telemetry.Position, value float32) { p.SetEngineBurning(i, value) },
		}
	}
}

func DatarefCount() int {
	return len(datarefsMap)
}
