package medschedule

import "sort"

// livePointStatus 在给定调用时刻 now 下，判定一个“当前仍有效”的未记录点的状态。
func livePointStatus(o *order, p, now, w int64) Status {
	if o.active() {
		// 在执行：p+W 严格小于 now => 漏给（含可补给的最近一个漏给点）；否则待给。
		if p+w < now {
			return StatusMissed
		}
		return StatusPending
	}
	// 已停嘱：规则只按停嘱时刻分界，与查询时刻 now 无关。
	// p+W 严格小于 stop 时刻 => 保持为漏给；否则作废。
	if p+w < o.stoppedAt {
		return StatusMissed
	}
	return StatusVoid
}

// QueryPatient 查询患者 [lo, hi] 闭区间内计划点及状态。
// 结果仅由当前状态与区间决定，并与调用时刻 now 一致。
// 开销为 O(符合条件的计划点数 + 命中段数的对数定位)，不遍历历史点。
func (s *System) QueryPatient(now int64, patient string, lo, hi int64) ([]Point, error) {
	if !validNow(now) || !validateIDs(patient) || lo < 0 || hi < lo {
		return nil, errInvalid("invalid query parameters")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	var out []Point
	for _, o := range s.orders {
		if o.patient != patient || o.kind == freqPRN {
			continue
		}
		if o.kind == freqInterval {
			s.queryInterval(o, now, lo, hi, &out)
		} else {
			s.queryDaily(o, now, lo, hi, &out)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Time != out[j].Time {
			return out[i].Time < out[j].Time
		}
		if out[i].OrderID != out[j].OrderID {
			return out[i].OrderID < out[j].OrderID
		}
		return out[i].Status < out[j].Status
	})
	return out, nil
}

func (s *System) queryDaily(o *order, now, lo, hi int64, out *[]Point) {
	dailyCandidates(o.timesOfDay, o.createdAt, lo, hi, func(t int64) {
		st, recorded := o.records[t]
		if !recorded {
			st = livePointStatus(o, t, now, s.w)
		}
		*out = append(*out, Point{OrderID: o.id, Patient: o.patient, Drug: o.drug, Time: t, Status: st})
	})
}

// queryInterval 按段生成。各段覆盖范围为 [start_i, start_{i+1})，最后一段无上界。
// 用二分把完全落在 lo 之前的段跳过；不读取与区间无关的历史段。
func (s *System) queryInterval(o *order, now, lo, hi int64, out *[]Point) {
	segs := o.segs
	// 第一段 start <= lo 的段可能覆盖 lo；找最后一个 start <= lo 的段索引。
	idx := sort.Search(len(segs), func(i int) bool { return segs[i].start > lo }) - 1
	if idx < 0 {
		idx = 0
	}
	for i := idx; i < len(segs); i++ {
		seg := segs[i]
		if seg.start > hi {
			break
		}
		segHi := hi
		if seg.cut >= 0 && seg.cut-1 < segHi {
			segHi = seg.cut - 1
		}
		segLo := lo
		if seg.start > segLo {
			segLo = seg.start
		}
		last := i == len(segs)-1
		intervalCandidates(seg.start, o.h, segLo, segHi, func(t int64) {
			st, recorded := o.records[t]
			if !recorded {
				switch {
				case !last && t > seg.madeUpAt:
					st = StatusVoid // 补给点之后的点被重排作废
				case !last:
					// 补给点之前的点保留其自然状态（通常已漏给）。
					st = livePointStatus(o, t, now, s.w)
				default:
					st = livePointStatus(o, t, now, s.w)
				}
			}
			*out = append(*out, Point{OrderID: o.id, Patient: o.patient, Drug: o.drug, Time: t, Status: st})
		})
	}
}

// QueryPRN 查询某患者全部必要时医嘱的实际给药记录（按时刻升序）。
func (s *System) QueryPRN(now int64, patient string) ([]PRNDose, error) {
	if !validNow(now) || !validateIDs(patient) {
		return nil, errInvalid("invalid query parameters")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	var out []PRNDose
	for _, o := range s.orders {
		if o.patient != patient || o.kind != freqPRN {
			continue
		}
		for _, t := range o.prnDoses {
			out = append(out, PRNDose{OrderID: o.id, Patient: o.patient, Drug: o.drug, Time: t})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Time != out[j].Time {
			return out[i].Time < out[j].Time
		}
		return out[i].OrderID < out[j].OrderID
	})
	return out, nil
}
