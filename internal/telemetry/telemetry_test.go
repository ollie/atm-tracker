package telemetry

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSetEngineBurningLightsTheEngineItWasAsked(t *testing.T) {
	var p Position

	p.SetEngineBurning(3, true)

	assert.True(t, p.engineBurning[3])
	assert.Equal(t, 1, p.EnginesRunning)
}

func TestSetEngineBurningCountsRunningEngines(t *testing.T) {
	var p Position

	p.SetEngineBurning(0, true)
	p.SetEngineBurning(3, true)
	assert.Equal(t, 2, p.EnginesRunning)

	p.SetEngineBurning(3, false)
	assert.Equal(t, 1, p.EnginesRunning, "a shut-down engine stops counting")

	p.SetEngineBurning(0, false)
	assert.Zero(t, p.EnginesRunning, "engines off is the deboard signal")
}

func TestPayloadIsTotalMinusEmptyMinusFuel(t *testing.T) {
	var p Position

	p.SetTotalMassKg(22571.95)
	p.SetEmptyMassKg(10184.97)
	p.SetFuelKg(1000)
	assert.InDelta(t, 11386.98, p.PayloadKg, 0.01, "stations and weapon stores together")

	p.SetTotalMassKg(22571.95 - 924.875)
	assert.InDelta(t, 10462.1, p.PayloadKg, 0.01, "a dropped store leaves the payload")

	p.SetFuelKg(900)
	assert.InDelta(t, 10562.1, p.PayloadKg, 0.01, "burned fuel does not")
}

func TestRoundToInt(t *testing.T) {
	assert.Equal(t, 360, RoundToInt(359.6))
	assert.Equal(t, 0, RoundToInt(0.4))
}
