package coalesce_test

import (
	"ontology/coalesce"
	"ontology/rangespec"
)

// toSpecs 把现成闭区间转成 FromTo 规格，供直接驱动归一化。
func toSpecs(ivs []coalesce.Interval) []rangespec.Spec {
	out := make([]rangespec.Spec, len(ivs))
	for i, iv := range ivs {
		out[i] = rangespec.Spec{
			Kind:  rangespec.FromTo,
			Start: iv.Start,
			End:   iv.End,
		}
	}
	return out
}
