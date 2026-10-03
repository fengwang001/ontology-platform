// Package policy implements the fixed-order tail sampling decisions.
package policy

// Reason values returned by Decide.
const (
	ReasonError      = "Error"
	ReasonLatency    = "Latency"
	ReasonProb       = "Prob"
	ReasonSampledOut = "Sampled out"
)

// Hash maps a trace identifier to a uint32 bucket.
type Hash func(string) uint32

// Policy evaluates traces in the fixed order: Error, Latency, Probability.
type Policy struct {
	latencyThresholdMs int64
	probPer10k         int64
	hash               Hash
}

// New constructs a Policy from its thresholds (L, P) and hash function.
func New(latencyThresholdMs, probPer10k int64, hash Hash) *Policy {
	return &Policy{
		latencyThresholdMs: latencyThresholdMs,
		probPer10k:         probPer10k,
		hash:               hash,
	}
}

// Decide returns the verdict without considering quota.
// It returns (keep, reason). Prob is a keep candidate the sampler may reject
// with "Budget"; Sampled out is a hard drop.
func (p *Policy) Decide(traceID string, hasError bool, maxDurMs int64) (keep bool, reason string) {
	if hasError {
		return true, ReasonError
	}
	if maxDurMs >= p.latencyThresholdMs {
		return true, ReasonLatency
	}
	if int64(p.hash(traceID)%10000) < p.probPer10k {
		return true, ReasonProb
	}
	return false, ReasonSampledOut
}
