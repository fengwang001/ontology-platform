// Package breaker is the circuit-breaker state machine: Closed, Open,
// HalfOpen. Every state transition bumps the epoch by one.
package breaker

// State is the circuit-breaker state.
type State int

const (
	Closed State = iota
	Open
	HalfOpen
)

func (s State) String() string {
	switch s {
	case Closed:
		return "Closed"
	case Open:
		return "Open"
	case HalfOpen:
		return "HalfOpen"
	}
	return "Unknown"
}

// Breaker holds the state machine and the trip/recovery thresholds.
type Breaker struct {
	state           State
	epoch           uint64
	openedAt        int64
	probesIssued    int
	probesSucceeded int

	minCalls   int
	failPct    int
	slowPct    int
	probes     int
	openMillis int64
}

// New returns a Closed breaker at epoch 0.
func New(minCalls, failPct, slowPct, probes int, openMillis int64) *Breaker {
	return &Breaker{
		minCalls:   minCalls,
		failPct:    failPct,
		slowPct:    slowPct,
		probes:     probes,
		openMillis: openMillis,
	}
}

// State returns the current state.
func (b *Breaker) State() State { return b.state }

// Epoch returns the current epoch (number of transitions so far).
func (b *Breaker) Epoch() uint64 { return b.epoch }

// ProbesIssued returns how many HalfOpen probes have been issued.
func (b *Breaker) ProbesIssued() int { return b.probesIssued }

// Settle promotes Open to HalfOpen once the open duration has elapsed.
func (b *Breaker) Settle(now int64) {
	if b.state == Open && now >= b.openedAt+b.openMillis {
		b.toHalfOpen()
	}
}

// ProbeAllowed reports whether another HalfOpen probe may be issued.
func (b *Breaker) ProbeAllowed() bool {
	return b.state == HalfOpen && b.probesIssued < b.probes
}

// NoteProbe records the issuance of one HalfOpen probe.
func (b *Breaker) NoteProbe() { b.probesIssued++ }

// EvalWindow applies the Closed-state thresholds to window statistics and
// opens the breaker when failure or slow rate reaches its threshold.
func (b *Breaker) EvalWindow(count, failures, slows int, now int64) (opened bool) {
	if count < b.minCalls {
		return false
	}
	if failures*100 >= b.failPct*count || slows*100 >= b.slowPct*count {
		b.toOpen(now)
		return true
	}
	return false
}

// EvalProbe applies one HalfOpen probe result: a failed or slow probe
// reopens the breaker, otherwise the success counter advances and the
// breaker closes once all probes succeeded.
func (b *Breaker) EvalProbe(fail, slow bool, now int64) {
	if fail || slow {
		b.toOpen(now)
		return
	}
	b.probesSucceeded++
	if b.probesSucceeded >= b.probes {
		b.toClosed()
	}
}

func (b *Breaker) toOpen(now int64) {
	b.state = Open
	b.openedAt = now
	b.epoch++
}

func (b *Breaker) toHalfOpen() {
	b.state = HalfOpen
	b.probesIssued, b.probesSucceeded = 0, 0
	b.epoch++
}

func (b *Breaker) toClosed() {
	b.state = Closed
	b.epoch++
}
