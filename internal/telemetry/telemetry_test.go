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

func TestRoundToInt(t *testing.T) {
	assert.Equal(t, 360, RoundToInt(359.6))
	assert.Equal(t, 0, RoundToInt(0.4))
}
