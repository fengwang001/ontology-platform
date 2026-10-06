package battery

type FaultCause string

const (
	FaultOvercurrent  FaultCause = "overcurrent"
	FaultVoltageDelta FaultCause = "voltage_delta"
)

// latchState holds the set of currently latched fault causes. Several causes
// may be contributed by the same sample; insertion order is preserved without
// duplicates, so callers can list every distinct reason.
type latchState struct {
	causes []FaultCause
}

func newLatchState() *latchState { return &latchState{} }

func (l *latchState) latched() bool { return len(l.causes) > 0 }

func (l *latchState) add(c FaultCause) {
	for _, existing := range l.causes {
		if existing == c {
			return
		}
	}
	l.causes = append(l.causes, c)
}

func (l *latchState) clear() { l.causes = nil }
