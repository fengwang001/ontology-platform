package loto

import (
	"fmt"
	"sort"
	"strings"
)

// Energizable 以系统当前时钟判定设备是否可送电。只读，不改变任何状态与时钟。
func (s *System) Energizable(device string) (Decision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.energizableAt(device, s.clock)
}

// EnergizableAt 判定设备在时刻 t 是否可送电（虚拟判定逾期，不改变状态与时钟）。
// t 小于当前时钟时按当前时钟判定。
func (s *System) EnergizableAt(device string, t int64) (Decision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t < s.clock {
		t = s.clock
	}
	return s.energizableAt(device, t)
}

// energizableAt 判定逻辑：
// 设备可送电，当且仅当其依赖的全部隔离点上都没有锁，
// 并且不存在涉及该设备的、处于开工或试运行之外的占用态票。
// 开销只取决于该设备当前关联的占用态票数，与历史票总数无关。
func (s *System) energizableAt(device string, t int64) (Decision, error) {
	const op = "Energizable"
	if device == "" {
		return Decision{}, newErr(op, ErrInvalidParam, "设备编号不能为空")
	}
	if _, ok := s.cfg.DevicePoints[device]; !ok {
		return Decision{}, newErr(op, ErrNotFound, "设备 %s 不存在", device)
	}
	d := Decision{Device: device, Locks: s.deviceLocks[device]}
	for id := range s.deviceActive[device] {
		p := s.active[id]
		if p.stateAt(t).blocksEnergize() {
			d.Blocking = append(d.Blocking, id)
		}
	}
	sort.Ints(d.Blocking)
	d.OK = d.Locks == 0 && len(d.Blocking) == 0
	var reasons []string
	if d.Locks > 0 {
		reasons = append(reasons, fmt.Sprintf("依赖隔离点上仍有 %d 把锁", d.Locks))
	}
	if len(d.Blocking) > 0 {
		reasons = append(reasons, fmt.Sprintf("存在阻止送电的占用态票 %v", d.Blocking))
	}
	if d.OK {
		d.Reason = "全部隔离点无锁，且无开工/试运行之外的占用态票"
	} else {
		d.Reason = strings.Join(reasons, "；")
	}
	return d, nil
}
