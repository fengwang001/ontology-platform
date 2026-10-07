package reflog

// gcLocked reclaims every commit and content object that is not
// reachable from the live roots and whose freshness grace has
// elapsed. The caller must hold the write lock and must have
// validated now.
//
// Live roots are: every reference's current value, plus the old and
// new values of every unexpired log record. Reachability follows
// parent edges (commit -> commit) and content references (commit ->
// object). A candidate is reclaimed only when
// now-FirstWrittenAt >= FreshnessGrace (equality counts).
func (s *System) gcLocked(now int64) GCStats {
	roots := make(map[CommitID]bool)
	for _, l := range s.logs {
		if l.hasCurrent {
			roots[l.current] = true
		}
		l.forEachUnexpired(now, s.cfg, func(rec *Record) {
			if rec.Old != "" {
				roots[rec.Old] = true
			}
			if rec.New != "" {
				roots[rec.New] = true
			}
		})
	}

	live := make(map[CommitID]bool)
	stack := make([]CommitID, 0, len(roots))
	for id := range roots {
		stack = append(stack, id)
	}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if live[id] {
			continue
		}
		c, ok := s.store.commits[id]
		if !ok {
			continue
		}
		live[id] = true
		stack = append(stack, c.Parents...)
	}

	liveObjects := make(map[ObjectID]bool)
	for id := range live {
		for _, oid := range s.store.commits[id].Contents {
			liveObjects[oid] = true
		}
	}

	var stats GCStats
	for id, c := range s.store.commits {
		if live[id] || now-c.FirstWrittenAt < s.cfg.FreshnessGrace {
			continue
		}
		delete(s.store.commits, id)
		stats.Commits++
		stats.Bytes += c.Size
	}
	for id, o := range s.store.objects {
		if liveObjects[id] || now-o.FirstWrittenAt < s.cfg.FreshnessGrace {
			continue
		}
		delete(s.store.objects, id)
		stats.Objects++
		stats.Bytes += o.Size
	}
	return stats
}
