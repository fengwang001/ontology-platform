package battery

// sustain tracks an uninterrupted run of a boolean condition.
type sustain struct {
	running bool
	startMS int64
}

func (st *sustain) observe(nowMS int64, cond bool, requiredMS int64) bool {
	if !cond {
		st.running = false
		return false
	}
	if !st.running {
		st.running = true
		st.startMS = nowMS
	}
	return nowMS-st.startMS >= requiredMS
}

func (st *sustain) reset() { *st = sustain{} }
