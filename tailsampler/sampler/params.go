package sampler

import (
	"errors"

	"ontology/tailsampler/policy"
)

// Rejection causes, reported in this order: invalid parameter, clock
// regression, duplicate span in buffer. A rejected call changes no state.
var (
	ErrInvalidParam = errors.New("sampler: invalid parameter")
	ErrClock        = errors.New("sampler: clock regression")
	ErrDuplicate    = errors.New("sampler: duplicate span in buffer")
)

const (
	maxClock = 1_000_000_000_000 // now must stay within [0, 1e12]
	maxDurMs = 1_000_000_000     // durMs must stay within [0, 1e9]
)

// Params configures a Sampler. Ranges are enforced by New:
// W,Td,L in [0,1e9]; Sc in [1,1e4]; Nmax in [1,1e5]; Cmax in [0,1e6];
// P in [0,10000] (per-10000); Wb in [1,1e9]; Q in [0,1e9].
// Td==0 or Cmax==0 disables the decision cache entirely.
type Params struct {
	W    uint64 // silence period: Tick decides traces with lastSeen+W <= now
	Sc   uint64 // span count triggering an immediate decision
	Nmax uint64 // max buffered traces; overflow evicts (lastSeen,traceID)-min
	Td   uint64 // decision cache TTL
	Cmax uint64 // decision cache capacity
	L    uint64 // latency threshold in ms
	P    uint64 // probability threshold, per 10000
	Wb   uint64 // quota window length; window of t is floor(t/Wb)
	Q    uint64 // keep quota per window
	H    policy.HashFunc
}

func (p Params) valid() bool {
	return p.W <= 1e9 && p.Td <= 1e9 && p.L <= 1e9 &&
		p.Sc >= 1 && p.Sc <= 1e4 &&
		p.Nmax >= 1 && p.Nmax <= 1e5 &&
		p.Cmax <= 1e6 && p.P <= 10000 &&
		p.Wb >= 1 && p.Wb <= 1e9 && p.Q <= 1e9
}

// Decision is the keep/drop verdict for one trace, emitted in decision order.
type Decision struct {
	TraceID string
	Keep    bool
	Reason  string // policy.Reason* constants
	Spans   int    // spans of this trace covered by the decision
	At      uint64 // decision clock value
	Evicted bool   // true when forced by buffer overflow on Ingest
}

// New validates p and returns a ready Sampler.
func New(p Params) (*Sampler, error) {
	if !p.valid() {
		return nil, ErrInvalidParam
	}
	h := p.H
	if h == nil {
		h = policy.FNV1a32
	}
	s := &Sampler{p: p, hash: h, buf: make(map[string]*bufItem)}
	if p.Td > 0 && p.Cmax > 0 {
		s.cache = newDecisionCache(p.Td, p.Cmax)
	}
	return s, nil
}
