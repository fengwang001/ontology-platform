package auditlog

import "strconv"

// Checkpoint is a (record index, key) pair held by a verifier.
type Checkpoint struct {
	Index int64
	Key   uint64
}

// Report is the result of a verification pass.
//
// Kind is empty (KindOK) on success. Pos is the position of the first
// failure (equal to the number of entries m for tail truncation, or to the
// conflicting checkpoint index). Verified counts fully verified entries;
// Unverifiable is the number u = min(j0, m) of entries strictly before the
// smallest checkpoint index, which are never inspected. EvolveCalls and
// RekeyCalls count the key transitions applied during this single pass.
// Sealed is meaningful only when Kind is empty: the last verified record is
// a seal record.
type Report struct {
	Kind         string
	Pos          int64
	Verified     int64
	Unverifiable int64
	EvolveCalls  int64
	RekeyCalls   int64
	Sealed       bool
}

// OK reports whether verification found no error.
func (r Report) OK() bool { return r.Kind == KindOK }

// Verify checks the entry slice against a non-empty set of checkpoints in a
// single forward pass. Entries are addressed by slice position t. The
// smallest checkpoint index j0 defines the start: positions below j0 are
// unverifiable (counted in Unverifiable) and never inspected.
//
// At each position t the key held for index t is first compared with any
// additional checkpoint at index t (a mismatch is KindCheckpointConflict),
// then the entry is checked in this fixed order:
//
//  1. Index != t: Index > t is KindGap, Index < t is KindReplay;
//  2. the previous position was a seal record: KindAppendAfterSeal;
//  3. (only when t > j0) Ts < previous Ts: KindTimeRollback;
//  4. Typ outside {0,1,2}: KindTampered (mac is not called);
//  5. Tag != mac(k, t, Typ, Ts, Data): KindTampered;
//  6. Typ 1 or 2 with Data != decimal(t): KindContentMismatch.
//
// After the last entry, a checkpoint at m is compared with the final key
// (KindCheckpointConflict), and any checkpoint above m is KindTruncated at
// position m.
func (l *Log) Verify(entries []*Entry, checkpoints []Checkpoint) (Report, error) {
	return l.verify(entries, checkpoints)
}

func (l *Log) verify(entries []*Entry, checkpoints []Checkpoint) (r Report, err error) {
	cp, j0, ok := buildCheckpointMap(checkpoints)
	if !ok {
		return Report{}, ErrInvalidArg
	}

	m := int64(len(entries))
	r = Report{Unverifiable: min64(j0, m)}

	t := j0
	if t > m {
		t = m
	}
	k := cp[j0]
	var evolveCount, rekeyCount int64 // non-exported single-pass counters
	defer func() {
		r.EvolveCalls = evolveCount
		r.RekeyCalls = rekeyCount
	}()

	for ; t < m; t++ {
		// Additional checkpoint at t: must match the derived key.
		if ck, exists := cp[t]; exists && t != j0 {
			if ck != k {
				r.Kind = KindCheckpointConflict
				r.Pos = t
				return r, nil
			}
		}

		e := entries[t]
		if e == nil {
			r.Kind = KindTampered
			r.Pos = t
			return r, nil
		}

		// 1. sequence continuity.
		if e.Index != t {
			r.Kind = KindReplay
			if e.Index > t {
				r.Kind = KindGap
			}
			r.Pos = t
			return r, nil
		}
		// 2. nothing may follow a seal record.
		if t > j0 {
			if prev := entries[t-1]; prev != nil && prev.Typ == TypSeal {
				r.Kind = KindAppendAfterSeal
				r.Pos = t
				return r, nil
			}
			// 3. monotonic timestamps; no check at the start position.
			if prev := entries[t-1]; prev != nil && e.Ts < prev.Ts {
				r.Kind = KindTimeRollback
				r.Pos = t
				return r, nil
			}
		}
		// 4. unknown type is tampering without invoking mac.
		if e.Typ != TypData && e.Typ != TypSeal && e.Typ != TypRekey {
			r.Kind = KindTampered
			r.Pos = t
			return r, nil
		}
		// 5. authenticator.
		if e.Tag != l.mac(k, t, e.Typ, e.Ts, e.Data) {
			r.Kind = KindTampered
			r.Pos = t
			return r, nil
		}
		// 6. seal/rekey payload must be the decimal index.
		if (e.Typ == TypSeal || e.Typ == TypRekey) &&
			string(e.Data) != strconv.FormatInt(t, 10) {
			r.Kind = KindContentMismatch
			r.Pos = t
			return r, nil
		}

		r.Verified++
		if e.Typ == TypRekey {
			k = l.rekey(k, t)
			rekeyCount++
		} else {
			k = l.evolve(k)
			evolveCount++
		}
	}

	// Checkpoint exactly at the end position.
	if ck, exists := cp[m]; exists && m != j0 {
		if ck != k {
			r.Kind = KindCheckpointConflict
			r.Pos = m
			return r, nil
		}
	}

	// Any checkpoint beyond the end proves a truncated tail.
	if maxIndex(checkpoints) > m {
		r.Kind = KindTruncated
		r.Pos = m
		return r, nil
	}

	if r.Verified > 0 {
		if last := entries[m-1]; last != nil && last.Typ == TypSeal {
			r.Sealed = true
		}
	}
	return r, nil
}

// SelfVerify verifies a consistent snapshot of the log's own entries.
func (l *Log) SelfVerify(checkpoints []Checkpoint) (Report, error) {
	snap := l.snapshot()
	return l.verify(snap, checkpoints)
}

func buildCheckpointMap(cps []Checkpoint) (map[int64]uint64, int64, bool) {
	if len(cps) == 0 {
		return nil, 0, false
	}
	j0 := cps[0].Index
	m := make(map[int64]uint64, len(cps))
	for _, c := range cps {
		if c.Index < 0 {
			return nil, 0, false
		}
		if _, dup := m[c.Index]; dup {
			return nil, 0, false
		}
		m[c.Index] = c.Key
		if c.Index < j0 {
			j0 = c.Index
		}
	}
	return m, j0, true
}

func maxIndex(cps []Checkpoint) int64 {
	mx := cps[0].Index
	for _, c := range cps[1:] {
		if c.Index > mx {
			mx = c.Index
		}
	}
	return mx
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
