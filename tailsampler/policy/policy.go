// Package policy decides keep/drop for one buffered trace using a fixed
// rule order. It is a pure function layer: no quota, no cache, no clock.
package policy

// Decision reasons attached to every Decision emitted by the sampler.
const (
	ReasonError      = "Error"
	ReasonLatency    = "Latency"
	ReasonProb       = "Prob"
	ReasonSampledOut = "Sampled out"
	ReasonBudget     = "Budget"
)

// HashFunc maps a trace ID to a uint32. Inject to make tests deterministic.
type HashFunc func(string) uint32

// FNV1a32 is the default HashFunc: FNV-1a 32-bit.
func FNV1a32(s string) uint32 {
	const offset, prime = 2166136261, 16777619
	h := uint32(offset)
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= prime
	}
	return h
}

// Decide applies the fixed policy order:
//  1. any error span        -> keep (ReasonError)
//  2. maxDur >= L           -> keep (ReasonLatency)
//  3. H(traceID)%10000 < P  -> probabilistic candidate (ReasonProb)
//     otherwise             -> drop (ReasonSampledOut)
//
// ReasonProb is only a candidate: the sampler may still downgrade it to
// ReasonBudget when the window quota is exhausted.
func Decide(traceID string, seenErr bool, maxDur, L, P uint64, h HashFunc) (keep bool, reason string) {
	if seenErr {
		return true, ReasonError
	}
	if maxDur >= L {
		return true, ReasonLatency
	}
	if uint64(h(traceID)%10000) < P {
		return true, ReasonProb
	}
	return false, ReasonSampledOut
}
