package export

// SafeStart re-derives, purely from history records, a start position that is
// no later than the last truly confirmed end. It is used when the checkpoint
// medium is unreadable or ambiguous.
//
// The returned position is the end of the longest prefix of SEALED epochs
// whose records are provably contiguous: every integer sequence in an epoch's
// (previousEnd, sealedEnd] interval appears exactly once, with one unique
// write ID per sequence and a consistent sequence per ID. The first sealed
// epoch anchors at the minimum sequence it contains minus one, which is the
// earliest start it can prove on its own.
//
// If a hole is found, SafeStart stops strictly before it: it returns the safe
// prefix end together with a KindHistoryGap error. "Stop before the gap" can
// never skip a write; at worst the caller re-checks and re-emits records the
// consumer already saw, which the consumer de-duplicates by Write.ID.
func SafeStart(link string, h HistoryStore) (Position, error) {
	recs, err := h.Records(link)
	if err != nil {
		return 0, err
	}
	byEpoch := map[int][]Record{}
	var epochs []int
	for _, r := range recs {
		if _, ok := byEpoch[r.Epoch]; !ok {
			epochs = append(epochs, r.Epoch)
		}
		byEpoch[r.Epoch] = append(byEpoch[r.Epoch], r)
	}
	// Keep only sealed epochs, in ascending epoch order.
	sortInts(epochs)
	var sealedEpochs []int
	for _, e := range epochs {
		if _, ok, _ := h.SealedEnd(link, e); ok {
			sealedEpochs = append(sealedEpochs, e)
		}
	}
	if len(sealedEpochs) == 0 {
		// Nothing provable. Position 0 is the genesis start; it is not later
		// than any real confirmed end.
		return 0, nil
	}

	var safe Position = -1
	var prevEnd Position
	for idx, epoch := range sealedEpochs {
		end, _, _ := h.SealedEnd(link, epoch)
		rs := byEpoch[epoch]
		var wantStart Position
		if idx == 0 {
			if len(rs) == 0 {
				return 0, mkErr(KindHistoryGap, "link %s: sealed epoch %d has no records", link, epoch)
			}
			wantStart = rs[0].Seq - 1
		} else {
			// Sealed epochs chain by their declared ends. Epoch numbers of
			// abandoned, unsealed attempts are skipped on purpose: they carry
			// no confirmed position and may legitimately be absent.
			wantStart = prevEnd
		}
		if err := verifyContiguous(rs, wantStart, end); err != nil {
			if safe < 0 {
				return 0, err
			}
			return safe, err
		}
		safe = end
		prevEnd = end
	}
	return safe, nil
}

func verifyContiguous(rs []Record, start, end Position) error {
	if len(rs) == 0 {
		return mkErr(KindHistoryGap, "sealed interval (%d,%d] has no records", start, end)
	}
	seenSeq := make(map[Position]string, end-start)
	for _, r := range rs {
		if r.Seq <= start || r.Seq > end {
			return mkErr(KindHistoryGap, "record seq %d falls outside (%d,%d]", r.Seq, start, end)
		}
		if id, dup := seenSeq[r.Seq]; dup {
			return mkErr(KindHistoryGap, "seq %d appears twice (ids %q,%q)", r.Seq, id, r.ID)
		}
		seenSeq[r.Seq] = r.ID
	}
	// Records is sequence-sorted, so checking every expected position detects
	// erased records and permuted IDs in one pass.
	if Position(len(rs)) != end-start {
		return mkErr(KindHistoryGap, "interval (%d,%d] expects %d records, history has %d", start, end, end-start, len(rs))
	}
	ids := map[string]Position{}
	for _, r := range rs {
		if seq, ok := ids[r.ID]; ok {
			return mkErr(KindHistoryGap, "write id %q bound to both seq %d and %d", r.ID, seq, r.Seq)
		}
		ids[r.ID] = r.Seq
	}
	return nil
}

func sortInts(xs []int) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j-1] > xs[j]; j-- {
			xs[j-1], xs[j] = xs[j], xs[j-1]
		}
	}
}
