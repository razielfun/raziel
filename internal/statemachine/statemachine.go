package statemachine

import "fmt"

type State string

const (
	StateQueued    State = "queued"
	StateBuilding  State = "building"
	StateDeploying State = "deploying"
	StateReady     State = "ready"
	StateFailed    State = "failed"
	StateDestroyed State = "destroyed"
)

var allowed = map[State][]State{
	StateQueued:    {StateBuilding, StateDeploying, StateFailed, StateDestroyed},
	StateBuilding:  {StateDeploying, StateFailed},
	StateDeploying: {StateReady, StateFailed},
	StateReady:     {StateQueued, StateDestroyed},
	StateFailed:    {StateQueued, StateDestroyed},
	StateDestroyed: {},
}

func CanTransition(from, to State) bool {
	for _, s := range allowed[from] {
		if s == to {
			return true
		}
	}
	return false
}

func IsTerminal(s State) bool {
	return s == StateDestroyed
}

func IsValid(s State) bool {
	_, ok := allowed[s]
	return ok
}

func ValidateTransition(from, to State) error {
	if !IsValid(from) {
		return fmt.Errorf("invalid state: %q", from)
	}
	if !IsValid(to) {
		return fmt.Errorf("invalid state: %q", to)
	}
	if !CanTransition(from, to) {
		return fmt.Errorf("invalid transition: %s → %s", from, to)
	}
	return nil
}
