package alarm

func applyTrigger(state State) (State, bool) {
	switch state {
	case Normal, ReturnedUnacknowledged:
		return ActiveUnacknowledged, true
	case ActiveUnacknowledged, ActiveAcknowledged:
		return state, false
	default:
		return state, false
	}
}

func applyReturn(state State) (State, error) {
	switch state {
	case ActiveUnacknowledged:
		return ReturnedUnacknowledged, nil
	case ActiveAcknowledged:
		return Normal, nil
	default:
		return state, newError(StateNotAllowed, "return", "alarm is not active")
	}
}

func applyAcknowledge(state State) (State, error) {
	switch state {
	case ActiveUnacknowledged:
		return ActiveAcknowledged, nil
	case ReturnedUnacknowledged:
		return Normal, nil
	default:
		return state, newError(StateNotAllowed, "acknowledge", "state does not allow acknowledgement")
	}
}

func visibleState(state State) bool {
	return state != Normal
}
