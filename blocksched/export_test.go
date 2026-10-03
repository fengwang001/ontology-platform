package blocksched

import "fmt"

// DebugInvariants 仅供测试使用：重新扫描全部状态并核对计数与约束。
func (s *Scheduler) DebugInvariants() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	blockFlight := make([]int, s.b)
	total := 0
	for key, req := range s.inflight {
		p, ok := s.peers[key.id]
		if !ok {
			return fmt.Errorf("inflight %v has no peer record", key)
		}
		if p.banned {
			return fmt.Errorf("banned peer %q has inflight block %d", key.id, key.b)
		}
		if key.b < 0 || key.b >= s.b {
			return fmt.Errorf("inflight block out of range: %v", key)
		}
		if s.complete[key.b] {
			return fmt.Errorf("completed block %d still inflight for %q", key.b, key.id)
		}
		if got := p.inflight[key.b]; got != req {
			return fmt.Errorf("inflight index mismatch %v", key)
		}
		blockFlight[key.b]++
		total++
	}
	for id, p := range s.peers {
		n := 0
		for b, req := range p.inflight {
			n++
			if s.inflight[inflightKey{id: id, b: b}] != req {
				return fmt.Errorf("peer %q inflight %d missing in global map", id, b)
			}
			if _, failed := p.fails[b]; failed {
				return fmt.Errorf("peer %q has inflight on failed block %d", id, b)
			}
		}
		if n > s.capFor(p) {
			return fmt.Errorf("peer %q inflight %d exceeds cap %d", id, n, s.capFor(p))
		}
	}
	for b := 0; b < s.b; b++ {
		if blockFlight[b] != s.blockFlight[b] {
			return fmt.Errorf("block %d flight counter %d != recomputed %d",
				b, s.blockFlight[b], blockFlight[b])
		}
		if blockFlight[b] > s.m {
			return fmt.Errorf("block %d flight %d exceeds M=%d", b, blockFlight[b], s.m)
		}
	}
	if total != s.totalFlight {
		return fmt.Errorf("total flight counter %d != recomputed %d", s.totalFlight, total)
	}
	if total > s.g {
		return fmt.Errorf("total flight %d exceeds G=%d", total, s.g)
	}

	avail := make([]int, s.b)
	for _, p := range s.peers {
		if p.banned {
			continue
		}
		for b := 0; b < s.b; b++ {
			if p.have[b] {
				avail[b]++
			}
		}
	}
	for b := 0; b < s.b; b++ {
		if avail[b] != s.availCount[b] {
			return fmt.Errorf("block %d avail counter %d != recomputed %d",
				b, s.availCount[b], avail[b])
		}
	}
	for id := range s.banned {
		if p, ok := s.peers[id]; ok && !p.banned {
			return fmt.Errorf("peer %q in banned set but not marked banned", id)
		}
	}
	return nil
}
