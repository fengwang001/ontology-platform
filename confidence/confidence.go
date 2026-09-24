// Package confidence labels merged aggregates under partial shard failure.
package confidence

import (
	"fmt"
	"math"

	"ontology/combine"
	"ontology/fanout"
)

// Label is the confidence annotation for one aggregate.
type Label struct {
	Kind   combine.Kind
	Exact  bool
	Lower  float64 // inclusive lower bound of the true value
	Upper  float64 // inclusive upper bound of the true value
	Bound  float64 // finite side for one-sided labels
	Prefix int     // guaranteed TopK prefix length (TopK only)
	Text   string
}

// Assess labels every aggregate for the given merged values.
func Assess(r *fanout.Result, v combine.Values) map[combine.Kind]Label {
	allOK := combine.AllOK(r)
	b := combine.MissingBoundSum(r)
	out := map[combine.Kind]Label{
		combine.Count: numeric(combine.Count, float64(v.Count), allOK, float64(v.Count), math.Inf(1)),
		combine.Sum:   numeric(combine.Sum, v.Sum, allOK, v.Sum, math.Inf(1)),
		combine.Max:   numeric(combine.Max, v.Max, allOK, v.Max, math.Inf(1)),
		combine.Min:   minLabel(v, allOK),
		combine.TopK:  topKLabel(v, allOK, b),
	}
	return out
}

func numeric(k combine.Kind, cur float64, exact bool, lo, hi float64) Label {
	l := Label{Kind: k, Lower: lo, Upper: hi}
	if exact {
		l.Exact = true
		l.Upper = cur
		l.Bound = cur
		l.Text = fmt.Sprintf("exact %s=%s", name(k), num(cur))
		return l
	}
	l.Bound = cur
	if math.IsInf(hi, 1) {
		l.Text = fmt.Sprintf("%s >= %s", name(k), num(cur))
	} else {
		l.Text = fmt.Sprintf("%s <= %s", name(k), num(cur))
	}
	return l
}

func minLabel(v combine.Values, allOK bool) Label {
	if !v.HasMin {
		return Label{Kind: combine.Min, Exact: true, Text: "exact Min=empty"}
	}
	if allOK {
		return numeric(combine.Min, v.Min, true, v.Min, v.Min)
	}
	// true Min <= observed Min; lower side is unbounded.
	l := Label{Kind: combine.Min, Lower: math.Inf(-1), Upper: v.Min, Bound: v.Min}
	l.Text = fmt.Sprintf("Min <= %s", num(v.Min))
	return l
}

func topKLabel(v combine.Values, allOK bool, missingBound float64) Label {
	l := Label{Kind: combine.TopK}
	if allOK {
		l.Exact = true
		l.Prefix = len(v.TopK)
		l.Text = fmt.Sprintf("exact TopK[%d]", len(v.TopK))
		return l
	}
	p := 0
	for _, rec := range v.TopK {
		if rec.V > missingBound {
			p++
		}
	}
	l.Prefix = p
	if p == len(v.TopK) && len(v.TopK) > 0 {
		l.Text = fmt.Sprintf("TopK membership certain (prefix=%d), order may shift", p)
	} else {
		l.Text = fmt.Sprintf("TopK guaranteed prefix=%d", p)
	}
	return l
}

func name(k combine.Kind) string {
	switch k {
	case combine.Count:
		return "Count"
	case combine.Sum:
		return "Sum"
	case combine.Min:
		return "Min"
	case combine.Max:
		return "Max"
	case combine.TopK:
		return "TopK"
	}
	return "?"
}

func num(f float64) string {
	if f == float64(int64(f)) {
		return fmt.Sprintf("%d", int64(f))
	}
	return fmt.Sprintf("%g", f)
}
