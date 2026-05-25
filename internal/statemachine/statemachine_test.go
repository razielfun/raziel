package statemachine_test

import (
	"testing"

	"github.com/raziel-ai/raziel/internal/statemachine"
	"github.com/stretchr/testify/assert"
)

func TestCanTransition(t *testing.T) {
	valid := [][2]statemachine.State{
		{statemachine.StateQueued, statemachine.StateBuilding},
		{statemachine.StateQueued, statemachine.StateFailed},
		{statemachine.StateBuilding, statemachine.StateDeploying},
		{statemachine.StateBuilding, statemachine.StateFailed},
		{statemachine.StateDeploying, statemachine.StateReady},
		{statemachine.StateDeploying, statemachine.StateFailed},
		{statemachine.StateReady, statemachine.StateDestroyed},
		{statemachine.StateFailed, statemachine.StateQueued},
		{statemachine.StateFailed, statemachine.StateDestroyed},
	}
	for _, tt := range valid {
		assert.True(t, statemachine.CanTransition(tt[0], tt[1]),
			"%s → %s should be valid", tt[0], tt[1])
	}

	invalid := [][2]statemachine.State{
		{statemachine.StateQueued, statemachine.StateReady},
		{statemachine.StateReady, statemachine.StateBuilding},
		{statemachine.StateDestroyed, statemachine.StateQueued},
		{statemachine.StateBuilding, statemachine.StateQueued},
	}
	for _, tt := range invalid {
		assert.False(t, statemachine.CanTransition(tt[0], tt[1]),
			"%s → %s should be invalid", tt[0], tt[1])
	}
}

func TestIsTerminal(t *testing.T) {
	assert.True(t, statemachine.IsTerminal(statemachine.StateDestroyed))
	assert.False(t, statemachine.IsTerminal(statemachine.StateReady))
	assert.False(t, statemachine.IsTerminal(statemachine.StateFailed))
}
