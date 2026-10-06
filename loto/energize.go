package loto

import (
	"fmt"
	"sort"
)

// CanEnergize 只读送电判定。
//
// 设备可送电，当且仅当：
//  1. 其依赖的全部隔离点上都没有（任何票的）物理在位锁；
//  2. 不存在涉及该设备、处于"开工(working)或试运行(trial)之外"的占用态票；
//     一旦票逾期（任何占用阶段，含 working/trial），同样阻断。
//
// 判定只访问：
//   - lockOwners[point]（按点维护的在位锁集合），
//   - activeByDevice[device]（按设备维护的占用票集合）；
//
// 两者都不含历史完成票，因此开销只取决于"该设备当前的占用票数/点上当前锁数"，
// 与历史票总数无关（见 scale_test.go 的两档对照）。
//
// 该查询不改变任何状态与时钟（不推进 overdue 标记），但为判定依据提供时间快照。
func (s *System) CanEnergize(device string, at int64) (bool, *EnergizeReport, error) {
	if device == "" {
		return false, nil, fail(InvalidParam, "device is required")
	}
	if at < 0 {
		return false, nil, fail(InvalidParam, "time must be non-negative")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if at < s.st.clock {
		return false, nil, fail(ClockRollback, "time %d < last accepted time %d", at, s.st.clock)
	}

	points, ok := s.st.devices[device]
	if !ok {
		return false, nil, fail(NotFound, "device %q not registered", device)
	}
	// 只读查询不更新时钟，只做回退可见性提示（错误顺序中时钟优先于对象，
	// 但对象已在上面判定；此处按只读语义，不校验与全局时钟关系也不推进）。

	rep := &EnergizeReport{Device: device, Energizable: true}

	ptList := make([]string, 0, len(points))
	for pt := range points {
		ptList = append(ptList, pt)
	}
	sort.Strings(ptList)
	for _, pt := range ptList {
		owners := s.st.lockOwners[pt]
		if len(owners) > 0 {
			rep.Energizable = false
			keys := make([]string, 0, len(owners))
			for k := range owners {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			rep.Reasons = append(rep.Reasons,
				fmt.Sprintf("BLOCK point=%s holds %d lock(s) %v -> isolated", pt, len(owners), keys))
		} else {
			rep.Reasons = append(rep.Reasons,
				fmt.Sprintf("OK    point=%s has no lock", pt))
		}
	}

	blocking := s.st.activeByDevice[device]
	ids := make([]string, 0, len(blocking))
	for id := range blocking {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		p := s.st.permits[id]
		if p.Overdue {
			rep.Energizable = false
			rep.Reasons = append(rep.Reasons,
				fmt.Sprintf("BLOCK permit=%s phase=%s overdue=true (overdue permit still occupies device)",
					id, p.Phase))
			continue
		}
		if p.Phase == PhaseWorking || p.Phase == PhaseTrial {
			rep.Reasons = append(rep.Reasons,
				fmt.Sprintf("OK    permit=%s phase=%s (work/trial does not block energize)", id, p.Phase))
			continue
		}
		rep.Energizable = false
		rep.Reasons = append(rep.Reasons,
			fmt.Sprintf("BLOCK permit=%s phase=%s overdue=%v occupies device while people may work",
				id, p.Phase, p.Overdue))
	}
	if len(ids) == 0 {
		rep.Reasons = append(rep.Reasons, "OK    no occupying permit involves the device")
	}
	return rep.Energizable, rep, nil
}
