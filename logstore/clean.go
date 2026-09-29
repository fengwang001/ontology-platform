package logstore

import "math/big"

// score returns the exact cost-benefit score of a sealed segment as a
// rational number: (1-u)*age/(1+u) with u = live/segSize, i.e.
// (segSize-live)*age / (segSize+live).
func (s *Store) score(seg *segment, live int) *big.Rat {
	age := s.clock - seg.maxTS
	num := new(big.Int).Mul(
		big.NewInt(int64(s.segSize-live)),
		new(big.Int).SetUint64(age),
	)
	den := big.NewInt(int64(s.segSize + live))
	return new(big.Rat).SetFrac(num, den)
}

// pickVictim selects the sealed segment with the highest cost-benefit score.
// Scores are compared as exact rationals; ties go to the lowest segment id.
// Returns nil when no sealed segment exists.
func (s *Store) pickVictim() *segment {
	var victim *segment
	var best *big.Rat
	for _, seg := range s.slots {
		if seg == nil || !seg.sealed {
			continue
		}
		live := s.liveBytes(seg)
		sc := s.score(seg, live)
		s.logf("candidate segment=%d live=%d/%d age=%d score=%s",
			seg.id, live, s.segSize, s.clock-seg.maxTS, sc.RatString())
		if best == nil || sc.Cmp(best) > 0 {
			victim, best = seg, sc
		}
	}
	if victim != nil {
		s.logf("victim segment=%d score=%s (ties broken by lowest id)",
			victim.id, best.RatString())
	}
	return victim
}

// cleanOne reclaims exactly one sealed segment, freeing one slot. Live blocks
// migrate to the log tail (the current segment) in original write order,
// keeping their original write timestamps. Cleaning only proceeds when it
// yields a net gain of one free slot; otherwise ErrSpaceExhausted is
// returned and nothing changes.
func (s *Store) cleanOne() error {
	victim := s.pickVictim()
	if victim == nil {
		s.logf("clean aborted: no sealed segment")
		return ErrSpaceExhausted
	}
	live := s.liveBytes(victim)
	if live > 0 {
		if live > s.current.remaining(s.segSize) {
			s.logf("clean aborted: victim %d live=%d does not fit tail remaining=%d; no net free slot",
				victim.id, live, s.current.remaining(s.segSize))
			return ErrSpaceExhausted
		}
		s.migrate(victim)
	}
	s.reclaim(victim)
	return nil
}

// migrate appends the live blocks of seg to the current segment in original
// write order, preserving original timestamps. Index and history entries keep
// pointing at the same blocks; only their segment location changes.
func (s *Store) migrate(seg *segment) {
	dead := seg.blocks[:0]
	for _, b := range seg.blocks {
		if !s.blockLive(b) {
			dead = append(dead, b)
			continue
		}
		b.seg = s.current.id
		s.current.blocks = append(s.current.blocks, b)
		s.current.used += b.size
		if b.ts > s.current.maxTS {
			s.current.maxTS = b.ts
		}
		s.logf("migrate key=%q tombstone=%v ts=%d size=%d from=%d to=%d",
			b.key, b.tombstone, b.ts, b.size, seg.id, b.seg)
	}
	seg.blocks = dead
}

// reclaim drops every block of seg from the index and history and frees the
// slot. Live blocks must already have been migrated.
func (s *Store) reclaim(seg *segment) {
	for _, b := range seg.blocks {
		if s.index[b.key] == b {
			delete(s.index, b.key)
		}
		hist := s.history[b.key]
		for i, hb := range hist {
			if hb == b {
				s.history[b.key] = append(hist[:i], hist[i+1:]...)
				break
			}
		}
		if len(s.history[b.key]) == 0 {
			delete(s.history, b.key)
		}
	}
	s.slots[seg.id] = nil
	s.logf("reclaim segment=%d freed slot", seg.id)
}
