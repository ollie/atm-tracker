package msfs

import (
	"bufio"
	"encoding/hex"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPacketsMatchNodeSimconnect(t *testing.T) {
	ours := map[string]*packet{
		"open":                openPacket(appName),
		"systemState":         systemStateRequest(stateRequestID, simEventName),
		"addToDataDefinition": addToDataDefinition(definitionID, "PLANE LATITUDE", "degrees"),
		"requestData":         requestDataEverySecond(requestID, definitionID),
		"subscribeSim":        subscribeToSystemEvent(simEvent, simEventName),
		"subscribePause":      subscribeToSystemEvent(pauseEvent, pauseEventName),
		"mapClientEvent":      mapClientEventToSimEvent(pauseOnEvent, pauseOnEventName),
		"transmitClientEvent": transmitClientEvent(pauseOnEvent),
	}

	f, err := os.Open("testdata/node-simconnect-packets.txt")
	require.NoError(t, err)
	defer f.Close()

	var sendID uint32
	for scanner := bufio.NewScanner(f); scanner.Scan(); sendID++ {
		name, theirs, _ := strings.Cut(scanner.Text(), " ")
		want, err := hex.DecodeString(theirs)
		require.NoError(t, err)

		p, ok := ours[name]
		require.Truef(t, ok, "no packet named %s", name)
		assert.Equalf(t, want, p.bytes(sendID), "%s differs from node-simconnect's encoding", name)
		delete(ours, name)
	}

	assert.Empty(t, ours, "packets the golden file does not cover")
}
