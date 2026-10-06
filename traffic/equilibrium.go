package traffic

// equilibrium 保存给定事件配置下每条路段的有效通行能力与受影响等级。
type equilibrium struct {
	cap   []float64
	level []int
}

// computeEquilibrium 求解通行能力上界（回溢定点）与等级 BFS。
func (s *Service) computeEquilibrium() *equilibrium {
	n := s.n
	eff := make([]float64, n)
	for i, l := range s.links {
		eff[i] = l.Capacity
	}

	// 事件削减：同一路段多个同时生效的事件取最大削减。
	for _, in := range s.inc {
		if in.ended || in.start > s.now+eps {
			continue
		}
		i := s.index[in.link]
		if c := s.links[i].Capacity * (1 - in.reduction); c < eff[i] {
			eff[i] = c
		}
	}

	// 回溢限制：full 路段迫使其每条直接上游的有效通行能力不超过自身。
	// 约束方向是上游 -> 下游，反复传播直至最小不动点；环路自然收敛。
	for {
		changed := false
		for i := 0; i < n; i++ {
			if !s.states[i].full {
				continue
			}
			bound := eff[i]
			for _, upID := range s.net.upstreamOf(s.links[i].ID) {
				j := s.index[upID]
				if bound < eff[j]-eps {
					eff[j] = bound
					changed = true
				}
			}
		}
		if !changed {
			break
		}
	}

	// 等级传播：事件路段为一级；沿 full 路段的上游方向逐级递增，多源取最小。
	level := make([]int, n)
	queue := make([]int, 0, n)
	for _, in := range s.inc {
		if in.ended || in.start > s.now+eps {
			continue
		}
		i := s.index[in.link]
		if level[i] == 0 || 1 < level[i] {
			level[i] = 1
		}
	}
	for i := 0; i < n; i++ {
		if level[i] == 1 {
			queue = append(queue, i)
		}
	}
	for head := 0; head < len(queue); head++ {
		i := queue[head]
		if !s.states[i].full {
			continue // 未回溢的路段不对上游施加限制，不产生二级及以上传播
		}
		nl := level[i] + 1
		for _, upID := range s.net.upstreamOf(s.links[i].ID) {
			j := s.index[upID]
			if level[j] == 0 || nl < level[j] {
				level[j] = nl
				queue = append(queue, j)
			}
		}
	}

	return &equilibrium{cap: eff, level: level}
}
