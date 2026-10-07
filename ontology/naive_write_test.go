package ontology_test

import (
	"sort"

	ont "ontology/ontology"
)

func (n *naiveEngine) write(sub ont.Subject, instID string, values map[string]ont.RawValue) naiveOutcome {
	out := naiveOutcome{}
	vals, exists := n.instances[instID]
	if !exists || !n.rowVisible(sub, vals, n.present[instID]) {
		out.errKind = "ErrInstanceNotFound"
		return out
	}
	for name := range values {
		if _, ok := n.ot.PropertyType(name); !ok {
			out.errKind = "ErrUnknownProperty"
			return out
		}
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		return propIndex(n.ot, names[i]) < propIndex(n.ot, names[j])
	})

	type pending struct {
		name     string
		incoming ont.RawValue
		wm       ont.WriteMaskFunc
		dt       ont.DataType
		drop     bool
	}
	var pend []pending
	for _, name := range names {
		var wAllow, wDeny bool
		var wms []ont.PropRule
		for _, r := range n.props {
			if r.Property != name || !naiveSel(r.Selector, sub) {
				continue
			}
			if r.WriteMask != nil {
				wms = append(wms, r)
			} else if r.HasWrite {
				if r.Writable {
					wAllow = true
				} else {
					wDeny = true
				}
			}
		}
		writable := wAllow
		if wAllow && wDeny {
			writable = n.pmode == ont.AllowOverrides
		}
		if len(wms) > 0 {
			writable = true
		}
		if !writable {
			if n.wmode == ont.WriteRejectAll {
				out.errKind = "ErrPropertyNotWritable"
				return out
			}
			pend = append(pend, pending{name: name, drop: true})
			continue
		}
		dt, _ := n.ot.PropertyType(name)
		var wm ont.WriteMaskFunc
		if len(wms) > 0 {
			winner := wms[0]
			for _, m := range wms[1:] {
				if m.ID < winner.ID {
					winner = m
				}
			}
			wm = winner.WriteMask
		}
		pend = append(pend, pending{name: name, incoming: values[name], wm: wm, dt: dt})
	}

	// Fixed priority across ALL targeted properties: masked-type
	// violations outrank plain invalid supplied values, regardless of
	// property order. Transforms run first.
	var invalidProp string
	for _, p := range pend {
		if p.drop || p.wm == nil {
			continue
		}
		mv, merr := p.wm(sub, p.incoming)
		if merr != nil || !naiveCheck(p.dt, mv) {
			out.errKind = "ErrMaskedTypeViolation"
			return out
		}
	}
	for _, p := range pend {
		if p.drop || p.wm != nil {
			continue
		}
		if !naiveCheck(p.dt, p.incoming) && invalidProp == "" {
			invalidProp = p.name
		}
	}
	if invalidProp != "" {
		out.errKind = "ErrInvalidValueType"
		return out
	}

	// Single atomic commit, declaration order.
	for _, p := range pend {
		if p.drop {
			out.dropped = append(out.dropped, p.name)
			continue
		}
		final := p.incoming
		if p.wm != nil {
			mv, _ := p.wm(sub, p.incoming)
			final = mv
		}
		vals[p.name] = final
		n.present[instID][p.name] = true
		out.applied = append(out.applied, p.name)
	}
	if len(out.applied) > 0 {
		n.versions[instID]++
	}
	out.applied = sortedCopy(out.applied)
	out.dropped = sortedCopy(out.dropped)
	out.version = n.versions[instID]

	snap := map[string]ont.RawValue{}
	for k, v := range vals {
		if n.present[instID][k] {
			snap[k] = v
		}
	}
	out.values = snap
	return out
}

// snapshot reads the FULL raw state via an omnipotent subject.
func (n *naiveEngine) snapshot(instID string) (map[string]ont.RawValue, bool) {
	vals, ok := n.instances[instID]
	if !ok {
		return nil, false
	}
	snap := map[string]ont.RawValue{}
	for k, v := range vals {
		if n.present[instID][k] {
			snap[k] = v
		}
	}
	return snap, true
}
