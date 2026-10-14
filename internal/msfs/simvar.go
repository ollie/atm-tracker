package msfs

import "github.com/ollie/atm-tracker/internal/telemetry"

const (
	latitude = iota
	longitude
	magneticHeading
	trueHeading
	track
	altitude
	heightAgl
	groundSpeed
	indicatedAirspeed
	trueAirspeed
	mach
	totalWeight
	emptyWeight
	fuelWeight
	onGround
	flapsHandle
	numEngines
	simulationRate
	engineCombustionAt
)

type simVar struct {
	name  string
	units string
}

var simVars = []simVar{
	latitude:          {"PLANE LATITUDE", "degrees"},
	longitude:         {"PLANE LONGITUDE", "degrees"},
	magneticHeading:   {"PLANE HEADING DEGREES MAGNETIC", "degrees"},
	trueHeading:       {"PLANE HEADING DEGREES TRUE", "degrees"},
	track:             {"GPS GROUND TRUE TRACK", "degrees"},
	altitude:          {"PLANE ALTITUDE", "feet"},
	heightAgl:         {"PLANE ALT ABOVE GROUND", "feet"},
	groundSpeed:       {"GROUND VELOCITY", "knots"},
	indicatedAirspeed: {"AIRSPEED INDICATED", "knots"},
	trueAirspeed:      {"AIRSPEED TRUE", "knots"},
	mach:              {"AIRSPEED MACH", "mach"},
	totalWeight:       {"TOTAL WEIGHT", "kilograms"},
	emptyWeight:       {"EMPTY WEIGHT", "kilograms"},
	fuelWeight:        {"FUEL TOTAL QUANTITY WEIGHT", "kilograms"},
	onGround:          {"SIM ON GROUND", "bool"},
	flapsHandle:       {"FLAPS HANDLE PERCENT", "percent over 100"},
	numEngines:        {"NUMBER OF ENGINES", "number"},
	simulationRate:    {"SIMULATION RATE", "number"},

	engineCombustionAt:     {"GENERAL ENG COMBUSTION:1", "bool"},
	engineCombustionAt + 1: {"GENERAL ENG COMBUSTION:2", "bool"},
	engineCombustionAt + 2: {"GENERAL ENG COMBUSTION:3", "bool"},
	engineCombustionAt + 3: {"GENERAL ENG COMBUSTION:4", "bool"},
	engineCombustionAt + 4: {"GENERAL ENG COMBUSTION:5", "bool"},
	engineCombustionAt + 5: {"GENERAL ENG COMBUSTION:6", "bool"},
	engineCombustionAt + 6: {"GENERAL ENG COMBUSTION:7", "bool"},
	engineCombustionAt + 7: {"GENERAL ENG COMBUSTION:8", "bool"},
}

func applyData(p *telemetry.Position, v []float64) {
	p.Lat = float32(v[latitude])
	p.Lon = float32(v[longitude])
	p.MagneticHeading = round(v[magneticHeading])
	p.TrueHeading = round(v[trueHeading])
	p.Track = round(v[track])
	p.AltitudeFt = round(v[altitude])
	p.HeightAglFt = round(v[heightAgl])
	p.GroundspeedKt = telemetry.Kts(round(v[groundSpeed]))
	p.IndicatedAirspeedKt = telemetry.Kts(round(v[indicatedAirspeed]))
	p.TrueAirspeedKt = telemetry.Kts(round(v[trueAirspeed]))
	p.Mach = float32(v[mach])
	p.PayloadKg = float32(v[totalWeight] - v[emptyWeight] - v[fuelWeight])
	p.FuelKg = float32(v[fuelWeight])
	p.OnGround = v[onGround] != 0
	p.FlapRatio = float32(v[flapsHandle])
	p.NumEngines = round(v[numEngines])
	p.SimSpeedMultiplier = round(v[simulationRate])
	p.GroundSpeedMultiplier = 1

	for i := range telemetry.MaxEngines {
		p.SetEngineBurning(i, v[engineCombustionAt+i] != 0)
	}
}

func round(v float64) int {
	return telemetry.RoundToInt(float32(v))
}
