// Package confidence derives per-aggregation confidence annotations
// from a (possibly partial) merge result.
package confidence

import (
	"fmt"

	"ontology/combine"
)

// Direction is how the reported value relates to the true value.
type Direction int

const (
	Exact     Direction = iota // all shards succeeded
	AtLeast                    // true value >= reported (Count/Sum, Max)
	AtMost                     // true value <= reported (Min)
	Uncertain                  // nothing can be said
)

// Assessment is the confidence annotation for one aggregation.
type Assessment struct {
	Direction     Direction
	Text          string
	TrustedPrefix int // TopK only: entries certain to belong to the true TopK
}

func exact(text string) Assessment { return Assessment{Direction: Exact, Text: text} }

// Assess evaluates one aggregation. See DESIGN.md for the derivation.
func Assess(agg combine.Agg, m combine.Merged) Assessment {
	partial := len(m.Failed) > 0
	switch agg {
	case combine.AggCount:
		if !partial {
			return exact(fmt.Sprintf("%d", m.Count))
		}
		return Assessment{Direction: AtLeast, Text: fmt.Sprintf(">= %d", m.Count)}
	case combine.AggSum:
		if !partial {
			return exact(fmt.Sprintf("%g", m.Sum))
		}
		return Assessment{Direction: AtLeast, Text: fmt.Sprintf(">= %g", m.Sum)}
	case combine.AggMin:
		if !partial {
			return exact(fmt.Sprintf("%g", m.Min))
		}
		if !m.HasMinMax {
			return Assessment{Direction: Uncertain, Text: "unknown"}
		}
		return Assessment{Direction: AtMost, Text: fmt.Sprintf("<= %g", m.Min)}
	case combine.AggMax:
		if !partial {
			return exact(fmt.Sprintf("%g", m.Max))
		}
		if !m.HasMinMax {
			return Assessment{Direction: Uncertain, Text: "unknown"}
		}
		return Assessment{Direction: AtLeast, Text: fmt.Sprintf(">= %g", m.Max)}
	case combine.AggTopK:
		j := 0
		if partial {
			for j < len(m.TopK) && m.TopK[j].Value > m.MissingBound {
				j++
			}
		} else {
			j = len(m.TopK)
		}
		dir := Exact
		if partial {
			dir = Uncertain
		}
		return Assessment{Direction: dir, TrustedPrefix: j,
			Text: fmt.Sprintf("trusted prefix %d/%d", j, len(m.TopK))}
	}
	return Assessment{Direction: Uncertain, Text: "unknown"}
}
