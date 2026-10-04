package push

import (
	"sort"

	"ontology/group"
	"ontology/policy"
)

// 以下方法仅供测试（同包测试）访问非导出状态。

// EffectiveConfig 加锁求值设备当前有效配置。
func (s *Service) EffectiveConfig(dev group.Name) Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return policy.EffectiveLocked(s.store, dev)
}

// PendingID 返回当前待确认任务编号（无则 0）。
func (s *Service) PendingID(dev group.Name) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d, ok := s.devs[dev]; ok {
		return d.pid
	}
	return 0
}

// AckedConfig 返回已确认配置的拷贝。
func (s *Service) AckedConfig(dev group.Name) Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d, ok := s.devs[dev]; ok {
		return cloneConfig(d.ack)
	}
	return nil
}

// PendingConfig 返回当前待确认任务内容的拷贝。
func (s *Service) PendingConfig(dev group.Name) Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d, ok := s.devs[dev]; ok && d.pid != 0 {
		return cloneConfig(d.pd)
	}
	return nil
}

// TaskStatus 按编号查询任务状态。
func (s *Service) TaskStatus(pid int) Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.tasks[pid]; ok {
		return t.Status
	}
	return -1
}

// NextPushID 返回下一个将分配的编号（即已分配个数）。
func (s *Service) NextPushID() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.next
}

// Recomputed 返回观测计数器。
func (s *Service) Recomputed() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.recomputed
}

// ResetRecomputed 清零观测计数器（测试用）。
func (s *Service) ResetRecomputed() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recomputed = 0
}

// NackCount 查询任务累计 Nack 次数。
func (s *Service) NackCount(pid int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.tasks[pid]; ok {
		return t.Nacks
	}
	return 0
}

// SortedDevices 返回现存设备名（字节序）。
func (s *Service) SortedDevices() []group.Name {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]group.Name, 0, len(s.devs))
	for d := range s.devs {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// HasDevice 判断设备是否存在。
func (s *Service) HasDevice(dev group.Name) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.devs[dev]
	return ok
}

// CheckInvariant 校验：有 Pd ⇔ E!=A 且 Pd==E；任务编号连续无洞；
// 每个非 Pending 任务结局唯一。返回不满足时的描述。
func (s *Service) CheckInvariant() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, d := range s.devs {
		e := policy.EffectiveLocked(s.store, name)
		eqA := mapEqual(e, d.ack)
		hasPd := d.pid != 0
		if eqA == hasPd {
			return "device " + string(name) + ": Pd presence mismatch with E==A"
		}
		if hasPd {
			if !mapEqual(e, d.pd) {
				return "device " + string(name) + ": Pd content != E"
			}
			if t := s.tasks[d.pid]; t == nil || t.Status != Pending {
				return "device " + string(name) + ": pending task missing or closed"
			}
		}
	}
	for pid := 1; pid <= s.next; pid++ {
		if s.tasks[pid] == nil {
			return "push id hole at " + itoa(pid)
		}
	}
	return ""
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	var b [20]byte
	i := len(b)
	for n != 0 {
		i--
		d := n % 10
		if d < 0 {
			d = -d
		}
		b[i] = byte('0' + d)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
