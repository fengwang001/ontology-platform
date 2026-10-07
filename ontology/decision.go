package ontology

import "sort"

// rowVerdict is the merged row-level conclusion plus its evidence.
type rowVerdict struct {
	visible bool
	effect  Effect
	matched []RowPolicy
	touched int
}

// propertyVerdict is the per-property merged conclusion plus evidence.
type propertyVerdict struct {
	readable   bool
	writable   bool
	mask       MaskFunc
	maskName   string
	maskPolicy string
	basis      []string
}

// decideRow evaluates row candidates against RAW values only. It never reads
// masked values and never consults property-level read verdicts.
func decideRow(candidates []RowPolicy, mode MergeMode, fallback Effect, raw Instance, types map[string]DeclaredType) rowVerdict {
	var effects []Effect
	var matched []RowPolicy
	for _, p := range candidates {
		if p.Predicate.eval(raw.Values, types) {
			matched = append(matched, p)
			effects = append(effects, p.Effect)
		}
	}
	effect := mergeEffects(effects, mode, fallback)
	return rowVerdict{
		visible: effect == EffectAllow,
		effect:  effect,
		matched: matched,
		touched: len(candidates),
	}
}

// decideProperty independently merges read and write conclusions for one
// property. Property "*" candidates match the property but stay scoped to
// it. Mask selection is deterministic: among ALL matched policies that carry
// a mask, the one with the lexicographically smallest policy ID wins, so the
// verdict cannot depend on registration order.
func decideProperty(candidates []PropertyPolicy, property string, mode MergeMode, defaultRead, defaultWrite Effect) propertyVerdict {
	var readEffects []Effect
	var writeEffects []Effect
	var basis []string
	var masked []PropertyPolicy
	for _, p := range candidates {
		if p.Property != propertyAll && p.Property != property {
			continue
		}
		basis = append(basis, p.ID)
		if p.Read != nil {
			readEffects = append(readEffects, *p.Read)
		}
		if p.Write != nil {
			writeEffects = append(writeEffects, *p.Write)
		}
		if p.Mask != nil {
			masked = append(masked, p)
		}
	}
	verdict := propertyVerdict{
		readable: mergeEffects(readEffects, mode, defaultRead) == EffectAllow,
		writable: mergeEffects(writeEffects, mode, defaultWrite) == EffectAllow,
		basis:    basis,
	}
	if len(masked) > 0 {
		sort.Slice(masked, func(i, j int) bool { return masked[i].ID < masked[j].ID })
		verdict.mask = masked[0].Mask
		verdict.maskName = masked[0].MaskName
		verdict.maskPolicy = masked[0].ID
	}
	sort.Strings(verdict.basis)
	return verdict
}

const propertyAll = "*"
