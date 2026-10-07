package ontology_test

import (
	"sort"

	ont "ontology/ontology"
)

func (n *naiveEngine) rowVisible(sub ont.Subject, vals map[string]ont.RawValue, pres map[string]bool) bool {
	var allow, deny bool
	for _, r := range n.rows {
		if !naiveSel(r.Selector, sub) {
			continue
		}
		match := true
		for _, p := range r.Predicates {
			if !n.pred(p, vals, pres) {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		if r.Effect == ont.EffectAllow {
			allow = true
		} else {
			deny = true
		}
	}
	if allow && deny {
		return n.rmode == ont.AllowOverrides
	}
	return allow
}

func (n *naiveEngine) read(sub ont.Subject, instID string, proj []string) naiveOutcome {
	out := naiveOutcome{view: map[string]string{}}
	vals, exists := n.instances[instID]
	if !exists || !n.rowVisible(sub, vals, n.present[instID]) {
		out.errKind = "ErrInstanceNotFound"
		return out
	}
	out.visible = true

	names := []string{}
	if len(proj) == 0 {
		for _, p := range n.ot.Properties() {
			names = append(names, p.Name)
		}
	} else {
		seen := map[string]bool{}
		for _, d := range n.ot.Properties() {
			for _, q := range proj {
				if q == d.Name && !seen[q] {
					seen[q] = true
					names = append(names, q)
				}
			}
		}
		for _, q := range proj {
			if !seen[q] {
				out.errKind = "ErrUnknownProperty"
				return out
			}
		}
	}

	for _, name := range names {
		var rAllow, rDeny bool
		var masks []ont.PropRule
		for _, r := range n.props {
			if r.Property != name || !naiveSel(r.Selector, sub) {
				continue
			}
			if r.Mask != nil {
				masks = append(masks, r)
			} else if r.HasRead {
				if r.Readable {
					rAllow = true
				} else {
					rDeny = true
				}
			}
		}
		state := "none"
		var mask ont.MaskFunc
		if len(masks) > 0 {
			winner := masks[0]
			for _, m := range masks[1:] {
				if m.ID < winner.ID {
					winner = m
				}
			}
			state, mask = "masked", winner.Mask
		} else if rAllow && rDeny {
			if n.pmode == ont.AllowOverrides {
				state = "raw"
			} else {
				state = "hidden"
			}
		} else if rAllow {
			state = "raw"
		} else if rDeny {
			state = "hidden"
		}

		switch state {
		case "raw":
			if n.present[instID][name] {
				out.view[name] = "raw:" + naiveValStr(vals[name])
			} else {
				out.absent = append(out.absent, name)
			}
		case "masked":
			if !n.present[instID][name] {
				out.absent = append(out.absent, name)
				break
			}
			mv, merr := mask(sub, vals[name])
			dt, _ := n.ot.PropertyType(name)
			if merr != nil || !naiveCheck(dt, mv) {
				out.errKind = "ErrMaskedTypeViolation"
				out.view = nil
				out.absent = nil
				out.redacted = nil
				return out
			}
			out.view[name] = "mask:" + naiveValStr(mv)
		default:
			out.redacted = append(out.redacted, name)
		}
	}
	out.redacted = sortedCopy(out.redacted)
	out.absent = sortedCopy(out.absent)
	return out
}

var _ = sort.Strings
