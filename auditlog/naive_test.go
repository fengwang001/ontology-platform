package auditlog

import "strconv"

// naiveVerify is the independent reference implementation. For every
// checkpoint it needs, it re-derives the key from the smallest checkpoint by
// walking the transitions from scratch (never from a single running key),
// then compares. Entry checks use the exact fixed order of the spec.
func naiveVerify(entries []*Entry, cps []Checkpoint, ev EvolveFunc, rk RekeyFunc, mc MacFunc) (Report, error) {
	if len(cps) == 0 {
		return Report{}, ErrInvalidArg
	}
	j0 := cps[0].Index
	cp := map[int64]uint64{}
	for _, c := range cps {
		if c.Index < 0 {
			return Report{}, ErrInvalidArg
		}
		if _, dup := cp[c.Index]; dup {
			return Report{}, ErrInvalidArg
		}
		cp[c.Index] = c.Key
		if c.Index < j0 {
			j0 = c.Index
		}
	}
	m := int64(len(entries))
	r := Report{Unverifiable: min64(j0, m)}

	// keyAt re-walks every transition from j0 for each requested index,
	// i.e. the naive "evolve again from the checkpoint" strategy.
	keyAt := func(idx int64) uint64 {
		k := cp[j0]
		for s := j0; s < idx; s++ {
			if entries[s].Typ == TypRekey {
				k = rk(k, s)
			} else {
				k = ev(k)
			}
		}
		return k
	}

	for t := j0; t < m; t++ {
		if t != j0 {
			if ck, ok := cp[t]; ok {
				if ck != keyAt(t) {
					r.Kind, r.Pos = KindCheckpointConflict, t
					return r, nil
				}
			}
		}
		e := entries[t]
		if e.Index != t {
			r.Pos = t
			if e.Index > t {
				r.Kind = KindGap
			} else {
				r.Kind = KindReplay
			}
			return r, nil
		}
		if t > j0 && entries[t-1].Typ == TypSeal {
			r.Kind, r.Pos = KindAppendAfterSeal, t
			return r, nil
		}
		if t > j0 && e.Ts < entries[t-1].Ts {
			r.Kind, r.Pos = KindTimeRollback, t
			return r, nil
		}
		if e.Typ != TypData && e.Typ != TypSeal && e.Typ != TypRekey {
			r.Kind, r.Pos = KindTampered, t
			return r, nil
		}
		if e.Tag != mc(keyAt(t), t, e.Typ, e.Ts, e.Data) {
			r.Kind, r.Pos = KindTampered, t
			return r, nil
		}
		if (e.Typ == TypSeal || e.Typ == TypRekey) && string(e.Data) != strconv.FormatInt(t, 10) {
			r.Kind, r.Pos = KindContentMismatch, t
			return r, nil
		}
		r.Verified++
		if e.Typ == TypRekey {
			r.RekeyCalls++
		} else {
			r.EvolveCalls++
		}
	}

	if ck, ok := cp[m]; ok && m != j0 {
		if ck != keyAt(m) {
			r.Kind, r.Pos = KindCheckpointConflict, m
			return r, nil
		}
	}
	maxIdx := cps[0].Index
	for _, c := range cps {
		if c.Index > maxIdx {
			maxIdx = c.Index
		}
	}
	if maxIdx > m {
		r.Kind, r.Pos = KindTruncated, m
		return r, nil
	}
	if r.Verified > 0 && entries[m-1].Typ == TypSeal {
		r.Sealed = true
	}
	return r, nil
}
