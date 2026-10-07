package billing

// 朴素参考模型（与生产实现独立编写）：
// 不做增量维护，对操作日志从头重放；每次状态变化后都用“全体账单联合切分”的
// 方式重算全部已结算账期。生产代码只依赖 meter.go 的读数原语与 allocate.go 的
// 分摊原语；本模型重新组织重算流程，作为随机对照测试的独立预言机。

import "sort"

type naiveState struct {
	estGap   int64
	now      int64
	master   string
	caps     map[string]int64
	readings map[string][]*Reading // 每只表的读数（含换表合成段首）
	areas    map[string]int64
	meterOf  map[string]string
	order    []string
	occ      map[string][]Occupancy
	periods  [][2]int64
}

type naiveBill struct {
	self   map[string]int64
	shared map[string]int64
	master int64
}

type naiveResult struct {
	bills       map[[2]int64]naiveBill
	corrections []Correction
}

func replayNaive(ops []Op, estGap int64) naiveResult {
	s := &naiveState{
		estGap:   estGap,
		caps:     map[string]int64{},
		readings: map[string][]*Reading{},
		areas:    map[string]int64{},
		meterOf:  map[string]string{},
		occ:      map[string][]Occupancy{},
	}
	res := naiveResult{bills: map[[2]int64]naiveBill{}}
	// prev 保存每个账期上一轮重算值，用于产生更正差额。
	prev := map[[2]int64]naiveBill{}

	for _, op := range ops {
		switch op.Kind {
		case "add_meter":
			if !op.Accepted {
				continue
			}
			s.caps[op.Meter] = op.Cap
			s.readings[op.Meter] = nil
			if op.Est { // AddMeter 借用 Est 位表示 master
				s.master = op.Meter
			}
			s.now = op.Now
		case "add_household":
			if !op.Accepted {
				continue
			}
			s.areas[op.House] = op.Area
			s.meterOf[op.House] = op.Meter
			s.order = append(s.order, op.House)
			s.occ[op.House] = nil
			s.now = op.Now
		case "add_occupancy":
			if !op.Accepted {
				continue
			}
			s.occ[op.House] = append(s.occ[op.House], Occupancy{Start: op.S, End: op.E})
			sort.Slice(s.occ[op.House], func(i, j int) bool { return s.occ[op.House][i].Start < s.occ[op.House][j].Start })
			s.now = op.Now
		case "enter_reading":
			if !op.Accepted {
				continue
			}
			s.applyReading(op)
			s.now = op.Now
		case "change_meter":
			if !op.Accepted {
				continue
			}
			s.applyChange(op)
			s.now = op.Now
		case "settle":
			if !op.Accepted {
				continue
			}
			s.periods = append(s.periods, [2]int64{op.P0, op.P1})
			s.now = op.Now
		}
		// 每次接受的变更后，朴素重算全部已结算账期。
		if op.Accepted && (op.Kind == "enter_reading" || op.Kind == "change_meter") && len(s.periods) > 0 {
			next := s.recomputeAll()
			for _, p := range s.periods {
				old, had := prev[p]
				nb := next[p]
				if had {
					c := Correction{Period: p, Deltas: map[string]int64{}}
					var sd int64
					for _, h := range s.order {
						c.Deltas[h] = (nb.self[h] + nb.shared[h]) - (old.self[h] + old.shared[h])
						sd += nb.shared[h] - old.shared[h]
					}
					c.SharedDelta = sd
					res.corrections = append(res.corrections, c)
				}
				prev[p] = nb
				res.bills[p] = nb
			}
		}
		if op.Kind == "settle" && op.Accepted {
			nb := s.recomputeAll()[[2]int64{op.P0, op.P1}]
			prev[[2]int64{op.P0, op.P1}] = nb
			res.bills[[2]int64{op.P0, op.P1}] = nb
		}
	}
	return res
}

func (s *naiveState) applyReading(op Op) {
	rs := s.readings[op.Meter]
	cap := s.caps[op.Meter]
	if op.Est {
		rs = append(rs, &Reading{Time: op.P0, Value: op.Value, Estimated: true, cap: cap})
		s.readings[op.Meter] = rs
		naiveRecompute(rs)
		return
	}
	// 实抄：先删除被替代的尾部估抄，再插入。
	for len(rs) > 0 && rs[len(rs)-1].Estimated && rs[len(rs)-1].Time < op.P0 {
		rs = rs[:len(rs)-1]
	}
	idx := sort.Search(len(rs), func(i int) bool { return rs[i].Time >= op.P0 })
	r := &Reading{Time: op.P0, Value: op.Value, cap: cap}
	rs = append(rs, nil)
	copy(rs[idx+1:], rs[idx:])
	rs[idx] = r
	s.readings[op.Meter] = rs
	naiveRecompute(rs)
}

func (s *naiveState) applyChange(op Op) {
	rs := s.readings[op.Meter]
	oldCap := s.caps[op.Meter]
	rs = append(rs,
		&Reading{Time: op.P0, Value: op.Value, cap: oldCap},
		&Reading{Time: op.P0, Value: op.Value2, segStart: true, cap: op.Cap})
	s.readings[op.Meter] = rs
	s.caps[op.Meter] = op.Cap
	naiveRecompute(rs)
}

func naiveRecompute(rs []*Reading) {
	if len(rs) == 0 {
		return
	}
	rs[0].pos = 0
	for j := 0; j+1 < len(rs); j++ {
		cur, next := rs[j], rs[j+1]
		if next.segStart {
			next.pos = cur.pos
			continue
		}
		next.pos = cur.pos + rolloverDelta(cur.Value, next.Value, cur.cap)
	}
}

// recomputeAll 用与生产代码相同的归属/分摊规则，但以全量扫描方式计算每个账期。
func (s *naiveState) recomputeAll() map[[2]int64]naiveBill {
	out := map[[2]int64]naiveBill{}
	if len(s.periods) == 0 {
		return out
	}
	for _, p := range s.periods {
		p0, p1 := p[0], p[1]
		bill := naiveBill{self: map[string]int64{}, shared: map[string]int64{}}
		var subSum int64
		for _, h := range s.order {
			u := naiveUsage(s.readings[s.meterOf[h]], p0, p1, s.periods)
			bill.self[h] = u
			subSum += u
		}
		mu := naiveUsage(s.readings[s.master], p0, p1, s.periods)
		bill.master = mu
		shared := mu - subSum
		if shared < 0 {
			shared = 0 // 负公摊操作会被拒绝，日志中不出现；防御性处理
		}
		durs := make([]int64, len(s.order))
		for i, h := range s.order {
			var d int64
			for _, o := range s.occ[h] {
				l, r := max64(o.Start, p0), min64(o.End, p1)
				if r > l {
					d += r - l
				}
			}
			durs[i] = s.areas[h] * d
		}
		sh := naiveLargestRemainder(shared, durs)
		for i, h := range s.order {
			bill.shared[h] = sh[i]
		}
		out[p] = bill
	}
	return out
}

// naiveUsage 是独立于生产闭式切分的归属算法（逐时刻单位水位模拟）。
// 每个时刻单位只取 floor 级联基础量；跨度末端未被已结算账期覆盖时，
// 余量保持未归属。仅当末端点 e 落在某已结算账期内部时，该账期拿到全部余量。
func naiveUsage(rs []*Reading, p0, p1 int64, periods [][2]int64) int64 {
	if len(rs) == 0 {
		return 0
	}
	// 位置锚点 pos 已由 naiveRecompute 维护；逐跨度处理。
	var total int64
	for i := 0; i+1 < len(rs); i++ {
		a, b := rs[i], rs[i+1]
		if b.Time == a.Time {
			continue
		}
		if b.Time <= p0 || a.Time >= p1 {
			continue
		}
		q := b.pos - a.pos
		span := b.Time - a.Time
		base := make([]int64, span)
		var given int64
		endInside := false
		for t := a.Time; t < b.Time; t++ {
			if t == b.Time-1 {
				// 末端时刻单位所在账期（左闭右开：单位 t 属于 [t,t+1)）。
				endInside = periodContains(periods, t)
			}
			cum := q * (t + 1 - a.Time) / span
			d := cum - given
			base[t-a.Time] = d
			given = cum
		}
		var baseSum int64
		for t := a.Time; t < b.Time; t++ {
			if t >= p0 && t < p1 && periodContains(periods, t) {
				total += base[t-a.Time]
				baseSum += base[t-a.Time]
			}
		}
		// 末端点 e 在本账期内部：本账期拿 q 减去其之前（含本份基础）之外——
		// 即此前所有账期基础量之和之后的全部余量。
		if endInside && b.Time > p0 && b.Time < p1 {
			var priorBase int64
			for t := a.Time; t < b.Time && t < p0; t++ {
				if periodContains(periods, t) {
					priorBase += base[t-a.Time]
				}
			}
			total += q - priorBase - baseSum
		}
	}
	return total
}

func periodContains(periods [][2]int64, t int64) bool {
	for _, p := range periods {
		if t >= p[0] && t < p[1] {
			return true
		}
	}
	return false
}

// naiveLargestRemainder 是独立于 allocate.go 写法的最大余数法分摊：
// floor(total*w_i/S) 后，把余数单位逐一分给精确余数（含整数部分亏欠）最大的户，
// 平局取索引较小者。使用 float 仅做排序候选选取，亏欠比较走交叉乘法的整数。
func naiveLargestRemainder(total int64, w []int64) []int64 {
	n := len(w)
	out := make([]int64, n)
	var S int64
	for _, x := range w {
		S += x
	}
	if S == 0 {
		for i := range w {
			w[i] = 1
			S++
		}
	}
	var rem int64 = total
	for i := 0; i < n; i++ {
		out[i] = total * w[i] / S
		rem -= out[i]
	}
	for ; rem > 0; rem-- {
		best := -1
		var bestL, bestR int64 // 亏欠 = total*w_i - out_i*S
		for i := 0; i < n; i++ {
			l := total*w[i] - out[i]*S
			r := S
			if best < 0 || l*bestR > bestL*r {
				best, bestL, bestR = i, l, r
			}
		}
		out[best]++
	}
	return out
}
