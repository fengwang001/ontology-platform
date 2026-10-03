package blocksched

import "sort"

// Tick 推进时钟：移除所有 now-issued >= T 的在途请求。
// 每条过期请求使对应对端的 timeouts 加 1；该块不产生失败记录，可立即再分配。
// 返回过期请求，按 (issued, id, b) 升序。
func (s *Scheduler) Tick(now int64) (expired []Timeout, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if now < s.now {
		return nil, ErrClock
	}
	s.now = now

	for key, req := range s.inflight {
		if now-req.issued >= s.t {
			expired = append(expired, Timeout{
				Issued: req.issued,
				Peer:   key.id,
				Block:  key.b,
			})
		}
	}
	sort.Slice(expired, func(i, j int) bool {
		if expired[i].Issued != expired[j].Issued {
			return expired[i].Issued < expired[j].Issued
		}
		if expired[i].Peer != expired[j].Peer {
			return expired[i].Peer < expired[j].Peer
		}
		return expired[i].Block < expired[j].Block
	})

	for _, e := range expired {
		p := s.peers[e.Peer]
		s.removeRequest(e.Peer, e.Block)
		p.timeouts++
	}
	return expired, nil
}
