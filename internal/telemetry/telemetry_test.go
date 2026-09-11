package telemetry

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSetEngineBurningLightsTheEngineItWasAsked(t *testing.T) {
	var p Position

	p.SetEngineBurning(3, 1)

	assert.True(t, p.engineBurning[3])
	assert.Equal(t, 1, p.EnginesRunning)
}

func TestSetEngineBurningCountsRunningEngines(t *testing.T) {
	var p Position

	p.SetEngineBurning(0, 1)
	p.SetEngineBurning(3, 1)
	assert.Equal(t, 2, p.EnginesRunning)

	p.SetEngineBurning(3, 0)
	assert.Equal(t, 1, p.EnginesRunning, "a shut-down engine stops counting")

	p.SetEngineBurning(0, 0)
	assert.Zero(t, p.EnginesRunning, "engines off is the deboard signal")
}

func TestConversions(t *testing.T) {
	assert.Equal(t, 3281, MetersToFeet(1000))
	assert.Equal(t, Kts(194), MpsToKts(100))
	assert.Equal(t, 360, RoundToInt(359.6))
	assert.Equal(t, 0, RoundToInt(0.4))
}
