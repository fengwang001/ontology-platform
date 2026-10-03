package merge

import "ontology/schema"

var (
	errInvalid = schema.ErrInvalidArgument
	errName    = schema.ErrNameMismatch
	errIncomp  = schema.ErrIncompatible
	errOver    = schema.ErrOverflow
)

// Merge 把两个合法直方图无损合并到公共边界上。
func Merge(a, b schema.Hist) (schema.Hist, error) {
	if !a.Valid() || !b.Valid() {
		return schema.Hist{}, errInvalid
	}
	if a.Name != b.Name {
		return schema.Hist{}, errName
	}
	common := make([]int64, 0, min(len(a.Bounds), len(b.Bounds)))
	cmps := 0
	i, j := 0, 0
	buckets128 := make([]u128, 0, cap(common)+1)
	var pendingA, pendingB u128
	for i < len(a.Bounds) && j < len(b.Bounds) {
		cmps++
		switch {
		case a.Bounds[i] == b.Bounds[j]:
			common = append(common, a.Bounds[i])
			pa := add128(pendingA, a.Counts[i])
			pb := add128(pendingB, b.Counts[j])
			buckets128 = append(buckets128, addU128(pa, pb))
			pendingA, pendingB = u128{}, u128{}
			i++
			j++
		case a.Bounds[i] < b.Bounds[j]:
			pendingA = add128(pendingA, a.Counts[i])
			i++
		default:
			pendingB = add128(pendingB, b.Counts[j])
			j++
		}
	}
	for ; i < len(a.Bounds); i++ {
		pendingA = add128(pendingA, a.Counts[i])
	}
	for ; j < len(b.Bounds); j++ {
		pendingB = add128(pendingB, b.Counts[j])
	}
	va := add128(pendingA, a.Counts[len(a.Bounds)])
	vb := add128(pendingB, b.Counts[len(b.Bounds)])
	buckets128 = append(buckets128, addU128(va, vb))
	addCmp(cmps)
	if len(common) == 0 {
		return schema.Hist{}, errIncomp
	}
	merged := schema.Hist{Name: a.Name, Bounds: common, Counts: make([]uint64, len(common)+1)}
	var total u128
	for k, v := range buckets128 {
		if v.exceedsInt64() {
			return schema.Hist{}, errOver
		}
		total = addU128(total, v)
		if total.exceedsInt64() {
			return schema.Hist{}, errOver
		}
		merged.Counts[k] = v.lo
	}
	sum, ok := addInt64(a.Sum, b.Sum)
	if !ok {
		return schema.Hist{}, errOver
	}
	merged.Sum = sum
	return merged, nil
}

// MergeAll 对一个或多个同名直方图取全体边界交集后相加。
func MergeAll(hs []schema.Hist) (schema.Hist, error) {
	if len(hs) == 0 {
		return schema.Hist{}, errInvalid
	}
	for _, h := range hs {
		if !h.Valid() {
			return schema.Hist{}, errInvalid
		}
	}
	name := hs[0].Name
	for _, h := range hs[1:] {
		if h.Name != name {
			return schema.Hist{}, errName
		}
	}
	common := append([]int64(nil), hs[0].Bounds...)
	for _, h := range hs[1:] {
		next := make([]int64, 0, len(common))
		i, j := 0, 0
		for i < len(common) && j < len(h.Bounds) {
			if common[i] == h.Bounds[j] {
				next = append(next, common[i])
				i++
				j++
			} else if common[i] < h.Bounds[j] {
				i++
			} else {
				j++
			}
		}
		common = next
	}
	if len(common) == 0 {
		return schema.Hist{}, errIncomp
	}
	out := schema.Hist{Name: name, Bounds: common, Counts: make([]uint64, len(common)+1)}
	acc := make([]u128, len(common)+1)
	var total u128
	var sum int64
	for _, h := range hs {
		p := project(h.Bounds, common, h.Counts)
		for k, v := range p {
			acc[k] = addU128(acc[k], v)
		}
		var ok bool
		sum, ok = addInt64(sum, h.Sum)
		if !ok {
			return schema.Hist{}, errOver
		}
	}
	for k, v := range acc {
		if v.exceedsInt64() {
			return schema.Hist{}, errOver
		}
		total = addU128(total, v)
		if total.exceedsInt64() {
			return schema.Hist{}, errOver
		}
		out.Counts[k] = v.lo
	}
	out.Sum = sum
	return out, nil
}
