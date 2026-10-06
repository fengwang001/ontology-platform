package metering

import "sort"

// recomputeCorrections 在读数序列于 affectedFrom 之后发生变化后，
// 重算所有可能受影响的已结算账期，为公摊发生变化的账期生成更正。
// 更正只记录各户应付差额，不改动已结算账单本身；同一账期各户差额之和
// 恒等于该账期公摊重算前后之差。
func (s *Service) recomputeCorrections(affectedFrom int64) []Correction {
	if affectedFrom == noEffect {
		return nil
	}
	keys := make([]periodKey, 0, len(s.bills))
	for k := range s.bills {
		if k.end > affectedFrom {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].start != keys[j].start {
			return keys[i].start < keys[j].start
		}
		return keys[i].end < keys[j].end
	})
	var out []Correction
	for _, k := range keys {
		old := s.current[k] // 相对上一次重算视图取增量，保证多次更正不重复累计
		nb, err := s.computeBill(k.start, k.end, true)
		if err != nil {
			continue // 重算不会失败（允许负公摊）
		}
		sharedDelta := nb.SharedUsage - old.SharedUsage
		if sharedDelta == 0 {
			continue
		}
		oldShare := make(map[string]int64, len(old.Units))
		for _, u := range old.Units {
			oldShare[u.UnitID] = u.Share
		}
		var deltas []UnitDelta
		for _, u := range nb.Units {
			if d := u.Share - oldShare[u.UnitID]; d != 0 {
				deltas = append(deltas, UnitDelta{UnitID: u.UnitID, Delta: d})
			}
		}
		c := Correction{Start: k.start, End: k.end, SharedDelta: sharedDelta, Deltas: deltas}
		out = append(out, c)
		s.corrections = append(s.corrections, c)
		s.current[k] = nb
	}
	return out
}
