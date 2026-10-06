package ontology

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"math/rand"
	"os"
	"sort"
	"testing"
)

// naiveMeter 是朴素参考模型：用有序切片保存读数，每次操作整体扫描重算，
// 不与生产实现共享任何数据结构（仅共用 Reading/Meter/ErrorCode 等纯类型）。
// 所有判定规则独立按题面重新实现，作为 Treap 引擎的对照基准。
type naiveMeter struct {
	digits    int
	mult      int64
	rate      int64
	mod       uint64
	readings  []Reading // 按时刻严格有序
	point     string
	installAt int64
	removeAt  int64
}

type naivePoint struct {
	meterIDs  []string
	installAt []int64
	removeAt  []int64
}

type naiveModel struct {
	meters map[string]*naiveMeter
	points map[string]*naivePoint
	log    *os.File
}

func newNaiveModel(log *os.File) *naiveModel {
	return &naiveModel{
		meters: map[string]*naiveMeter{},
		points: map[string]*naivePoint{},
		log:    log,
	}
}

func (nm *naiveModel) record(format string, args ...any) {
	fmt.Fprintf(nm.log, "  [naive] "+format+"\n", args...)
}

func nDelta(prev, cur uint64, mod uint64) (uint64, bool) {
	if cur >= prev {
		return cur - prev, false
	}
	return mod + cur - prev, true
}

func nEdgeOK(delta uint64, mult int64, dt int64, rate int64) bool {
	return withinRationalLimit(delta, mult, dt, rate)
}

func (m *naiveMeter) find(at int64) int {
	return sort.Search(len(m.readings), func(i int) bool { return m.readings[i].Time >= at })
}

func (nm *naiveModel) createMeter(id string, digits int, mult, rate int64) error {
	if id == "" {
		return errf(ErrInvalidArgument, "电表标识为空")
	}
	if digits < 1 || digits > 18 || mult <= 0 || rate < 0 {
		return errf(ErrInvalidArgument, "静态参数非法")
	}
	if _, ok := nm.meters[id]; ok {
		return errf(ErrInvalidArgument, "电表已存在")
	}
	nm.meters[id] = &naiveMeter{digits: digits, mult: mult, rate: rate, mod: modulus(digits), installAt: 1, removeAt: 0}
	nm.record("CreateMeter(%s,digits=%d,mult=%d,rate=%d) -> ok", id, digits, mult, rate)
	return nil
}

func (nm *naiveModel) activeMeter(point string) bool {
	p := nm.points[point]
	if p == nil || len(p.meterIDs) == 0 {
		return false
	}
	return p.removeAt[len(p.removeAt)-1] == math.MaxInt64
}

func (nm *naiveModel) attach(point, meter string, at int64, initial uint64) error {
	if point == "" || meter == "" || !validTime(at) {
		return errf(ErrInvalidArgument, "参数非法")
	}
	m := nm.meters[meter]
	if m == nil || initial >= m.mod {
		return errf(ErrInvalidArgument, "电表不存在或显示值超位")
	}
	if nm.activeMeter(point) {
		return errf(ErrReadingScheduleConflict, "供电点已挂表")
	}
	if m.point != "" {
		return errf(ErrReadingScheduleConflict, "电表已挂他点")
	}
	if len(m.readings) > 0 && m.readings[len(m.readings)-1].Time > at {
		return errf(ErrReadingScheduleConflict, "早于已有读数")
	}
	if ex := m.find(at); ex < len(m.readings) && m.readings[ex].Time == at {
		if m.readings[ex].Kind != Actual || m.readings[ex].Value != initial {
			return errf(ErrReadingConflict, "同刻异读")
		}
		nm.record("Attach(%s,%s,t=%d,v=%d) -> idempotent", point, meter, at, initial)
		return nil
	}
	m.readings = append(m.readings, Reading{Time: at, Value: initial, Kind: Actual})
	m.point = point
	m.installAt = at
	m.removeAt = math.MaxInt64
	p := nm.points[point]
	if p == nil {
		p = &naivePoint{}
		nm.points[point] = p
	}
	p.meterIDs = append(p.meterIDs, meter)
	p.installAt = append(p.installAt, at)
	p.removeAt = append(p.removeAt, math.MaxInt64)
	nm.record("Attach(%s,%s,t=%d,v=%d) -> ok", point, meter, at, initial)
	return nil
}

func (nm *naiveModel) replace(point string, at int64, oldLast uint64, newMeter string, newInit uint64) error {
	if point == "" || newMeter == "" || !validTime(at) {
		return errf(ErrInvalidArgument, "参数非法")
	}
	p := nm.points[point]
	if p == nil || len(p.meterIDs) == 0 || !nm.activeMeter(point) {
		return errf(ErrReadingScheduleConflict, "供电点无活动表")
	}
	oldID := p.meterIDs[len(p.meterIDs)-1]
	old := nm.meters[oldID]
	nw := nm.meters[newMeter]
	if nw == nil || newMeter == oldID || oldLast >= old.mod || newInit >= nw.mod {
		return errf(ErrInvalidArgument, "表或显示值非法")
	}
	if at < old.readings[len(old.readings)-1].Time {
		return errf(ErrReadingScheduleConflict, "早于旧表最新读数")
	}
	if len(nw.readings) > 0 && at < nw.readings[len(nw.readings)-1].Time {
		return errf(ErrReadingScheduleConflict, "早于新表已有记录")
	}
	if nw.point != "" {
		return errf(ErrReadingScheduleConflict, "新表已挂")
	}
	oi := old.find(at)
	oldEst := false
	if oi < len(old.readings) && old.readings[oi].Time == at {
		ex := old.readings[oi]
		if ex.Kind == Actual && ex.Value != oldLast {
			return errf(ErrReadingConflict, "旧表同刻异值")
		}
		oldEst = ex.Kind == Estimated
		if oldEst {
			if oi > 0 {
				d, _ := nDelta(old.readings[oi-1].Value, oldLast, old.mod)
				if !nEdgeOK(d, old.mult, at-old.readings[oi-1].Time, old.rate) {
					return errf(ErrUnreasonable, "替换前驱区间")
				}
			}
			if oi+1 < len(old.readings) {
				d, _ := nDelta(oldLast, old.readings[oi+1].Value, old.mod)
				if !nEdgeOK(d, old.mult, old.readings[oi+1].Time-at, old.rate) {
					return errf(ErrUnreasonable, "替换后继区间")
				}
			}
		}
	}
	if ni := nw.find(at); ni < len(nw.readings) && nw.readings[ni].Time == at {
		ex := nw.readings[ni]
		if ex.Kind != Actual || ex.Value != newInit {
			return errf(ErrReadingConflict, "新表同刻异读")
		}
	}
	if (oi >= len(old.readings) || old.readings[oi].Time != at || oldEst) && oi > 0 {
		d, _ := nDelta(old.readings[oi-1].Value, oldLast, old.mod)
		if !nEdgeOK(d, old.mult, at-old.readings[oi-1].Time, old.rate) {
			return errf(ErrUnreasonable, "旧表末次区间")
		}
	}
	if oldEst {
		old.readings = append(old.readings[:oi], old.readings[oi+1:]...)
		oi = old.find(at)
	}
	if oi >= len(old.readings) || old.readings[oi].Time != at {
		old.readings = append(old.readings, Reading{})
		copy(old.readings[oi+1:], old.readings[oi:])
		old.readings[oi] = Reading{Time: at, Value: oldLast, Kind: Actual}
	}
	if ni := nw.find(at); ni >= len(nw.readings) || nw.readings[ni].Time != at {
		nw.readings = append(nw.readings, Reading{})
		copy(nw.readings[ni+1:], nw.readings[ni:])
		nw.readings[ni] = Reading{Time: at, Value: newInit, Kind: Actual}
	}
	old.removeAt = at
	old.point = ""
	nw.point = point
	nw.installAt = at
	nw.removeAt = math.MaxInt64
	p.removeAt[len(p.removeAt)-1] = at
	p.meterIDs = append(p.meterIDs, newMeter)
	p.installAt = append(p.installAt, at)
	p.removeAt = append(p.removeAt, math.MaxInt64)
	nm.record("Replace(%s,t=%d,old=%d,new=%s,init=%d) -> ok", point, at, oldLast, newMeter, newInit)
	return nil
}

func (nm *naiveModel) register(meter string, at int64, value uint64, kind ReadingKind) error {
	if meter == "" || !validTime(at) {
		return errf(ErrInvalidArgument, "参数非法")
	}
	m := nm.meters[meter]
	if m == nil || value >= m.mod {
		return errf(ErrInvalidArgument, "电表不存在或超位")
	}
	if at < m.installAt || at >= m.removeAt {
		return errf(ErrNotAttached, "不在挂接期内")
	}
	i := m.find(at)
	if i < len(m.readings) && m.readings[i].Time == at {
		ex := m.readings[i]
		switch {
		case ex.Kind == Actual && kind == Actual && ex.Value == value:
			return nil
		case ex.Kind == Actual:
			return errf(ErrReadingConflict, "同刻实抄")
		case kind == Estimated:
			return errf(ErrReadingConflict, "同刻估算")
		default:
			if i > 0 && m.readings[i-1].Time >= m.installAt && m.readings[i-1].Time <= m.removeAt {
				d, _ := nDelta(m.readings[i-1].Value, value, m.mod)
				if !nEdgeOK(d, m.mult, at-m.readings[i-1].Time, m.rate) {
					return errf(ErrUnreasonable, "替换前驱")
				}
			}
			if i+1 < len(m.readings) && m.readings[i+1].Time >= m.installAt && m.readings[i+1].Time <= m.removeAt {
				d, _ := nDelta(value, m.readings[i+1].Value, m.mod)
				if !nEdgeOK(d, m.mult, m.readings[i+1].Time-at, m.rate) {
					return errf(ErrUnreasonable, "替换后继")
				}
			}
			m.readings[i].Value, m.readings[i].Kind = value, Actual
			nm.record("Register(%s,t=%d,v=%d,%s) -> replaced estimate", meter, at, value, kind)
			return nil
		}
	}
	// 相邻读数只在当前挂接期 [installAt, removeAt] 内取（两端衔接点均含）。
	tourLo, tourHi := m.installAt, m.removeAt
	pidx, sidx := i-1, i
	for pidx >= 0 && (m.readings[pidx].Time < tourLo || m.readings[pidx].Time > tourHi) {
		pidx--
	}
	for sidx < len(m.readings) && (m.readings[sidx].Time < tourLo || m.readings[sidx].Time > tourHi) {
		sidx++
	}
	hasPred, hasSucc := pidx >= 0, sidx < len(m.readings)
	if kind == Estimated {
		if hasSucc {
			return errf(ErrReadingScheduleConflict, "估算乱序")
		}
	}
	if kind == Actual && hasPred && hasSucc {
		prevR, succR := m.readings[pidx], m.readings[sidx]
		_, oro := nDelta(prevR.Value, succR.Value, m.mod)
		_, r1 := nDelta(prevR.Value, value, m.mod)
		_, r2 := nDelta(value, succR.Value, m.mod)
		if boolCount(r1)+boolCount(r2) != boolCount(oro) {
			return errf(ErrReadingScheduleConflict, "翻转不守恒")
		}
	}
	if hasPred {
		d, _ := nDelta(m.readings[pidx].Value, value, m.mod)
		if !nEdgeOK(d, m.mult, at-m.readings[pidx].Time, m.rate) {
			return errf(ErrUnreasonable, "前驱区间")
		}
	}
	if hasSucc {
		d, _ := nDelta(value, m.readings[sidx].Value, m.mod)
		if !nEdgeOK(d, m.mult, m.readings[sidx].Time-at, m.rate) {
			return errf(ErrUnreasonable, "后继区间")
		}
	}
	m.readings = append(m.readings, Reading{})
	copy(m.readings[i+1:], m.readings[i:])
	m.readings[i] = Reading{Time: at, Value: value, Kind: kind}
	nm.record("Register(%s,t=%d,v=%d,%s) -> ok", meter, at, value, kind)
	return nil
}

func (nm *naiveModel) deleteEst(meter string, at int64) error {
	if meter == "" || !validTime(at) {
		return errf(ErrInvalidArgument, "参数非法")
	}
	m := nm.meters[meter]
	if m == nil {
		return errf(ErrInvalidArgument, "电表不存在")
	}
	if at < m.installAt || at >= m.removeAt {
		return errf(ErrNotAttached, "不在挂接期内")
	}
	i := m.find(at)
	if i >= len(m.readings) || m.readings[i].Time != at {
		return errf(ErrNoReading, "无读数")
	}
	if m.readings[i].Kind == Actual {
		return errf(ErrReadingConflict, "实抄不可删")
	}
	pi, si := i-1, i+1
	for pi >= 0 && (m.readings[pi].Time < m.installAt || m.readings[pi].Time > m.removeAt) {
		pi--
	}
	for si < len(m.readings) && (m.readings[si].Time < m.installAt || m.readings[si].Time > m.removeAt) {
		si++
	}
	if pi >= 0 && si < len(m.readings) {
		d, _ := nDelta(m.readings[pi].Value, m.readings[si].Value, m.mod)
		if !nEdgeOK(d, m.mult, m.readings[si].Time-m.readings[pi].Time, m.rate) {
			return errf(ErrUnreasonable, "合并区间")
		}
	}
	m.readings = append(m.readings[:i], m.readings[i+1:]...)
	nm.record("DeleteEst(%s,t=%d) -> ok", meter, at)
	return nil
}

func (nm *naiveModel) query(point string, from, to int64) (QueryResult, error) {
	if point == "" || !validTime(from) || !validTime(to) {
		return QueryResult{}, errf(ErrInvalidArgument, "参数非法")
	}
	if from > to {
		return QueryResult{}, errf(ErrInvalidArgument, "时刻倒置")
	}
	p := nm.points[point]
	noRead := func() (QueryResult, error) {
		return QueryResult{}, errf(ErrNoReading, "端点无读数")
	}
	if p == nil {
		return noRead()
	}
	segOf := func(t int64) int {
		idx := sort.Search(len(p.installAt), func(i int) bool { return p.installAt[i] > t }) - 1
		if idx < 0 || p.removeAt[idx] < t {
			return -1
		}
		return idx
	}
	lo, hi := segOf(from), segOf(to)
	if lo < 0 || hi < 0 {
		return noRead()
	}
	if lo > hi {
		lo, hi = hi, lo
	}
	total := big.NewInt(0)
	est := false
	for i := lo; i <= hi; i++ {
		m := nm.meters[p.meterIDs[i]]
		l, h := from, to
		if p.installAt[i] > l {
			l = p.installAt[i]
		}
		if p.removeAt[i] <= h {
			h = p.removeAt[i]
		}
		if l >= h {
			continue
		}
		rs := m.readings
		li := sort.Search(len(rs), func(k int) bool { return rs[k].Time >= l })
		ri := sort.Search(len(rs), func(k int) bool { return rs[k].Time > h }) - 1
		if li >= len(rs) || ri < 0 || rs[li].Time != l || rs[ri].Time != h {
			return noRead()
		}
		for k := li; k < ri; k++ {
			d, _ := nDelta(rs[k].Value, rs[k+1].Value, m.mod)
			total.Add(total, new(big.Int).Mul(new(big.Int).SetUint64(d), big.NewInt(m.mult)))
			if rs[k].Kind == Estimated || rs[k+1].Kind == Estimated {
				est = true
			}
		}
	}
	return QueryResult{Usage: total, ContainsEstimated: est}, nil
}

// 随机操作：与生产引擎执行同一条序列，逐条比对错误类别与查询结果，
// 并对任意三点验证可加性。日志打印每条输入、输出与判定依据。
func TestNaiveDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过耗时的随机对照")
	}
	logFile, err := os.Create("/tmp/ontology_differential.log")
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()

	rng := rand.New(rand.NewSource(20261006))
	const iterations = 3000
	for iter := 0; iter < 30; iter++ {
		e := NewEngine()
		nm := newNaiveModel(logFile)
		pointID := "P"
		meterN := 0
		meterIDs := []string{}
		newMeter := func() string {
			meterN++
			id := fmt.Sprintf("m%d", meterN)
			meterIDs = append(meterIDs, id)
			digits := 1 + rng.Intn(3)
			mult := int64(1 + rng.Intn(4))
			rate := int64(rng.Intn(30) + 5)
			fmt.Fprintf(logFile, "iter=%d CreateMeter(%s digits=%d mult=%d rate=%d)\n", iter, id, digits, mult, rate)
			e1 := e.CreateMeter(Meter{ID: id, Digits: digits, Multiplier: mult, MaxUsagePerUnitTime: rate})
			e2 := nm.createMeter(id, digits, mult, rate)
			assertSameErr(t, e1, e2)
			return id
		}
		first := newMeter()
		mustOK(t, e.Attach(pointID, first, 0, 0))
		mustOK(t, nm.attach(pointID, first, 0, 0))

		// 已登记的读数时刻，用于查询/乱序插入选点。
		timesByMeter := map[string][]int64{first: {0}}
		clock := int64(0)
		for step := 0; step < iterations; step++ {
			mid := meterIDs[rng.Intn(len(meterIDs))]
			kind := ReadingKind(rng.Intn(2))
			at := clock + int64(rng.Intn(8))
			if rng.Intn(3) == 0 && len(timesByMeter[mid]) > 1 {
				// 乱序：从已有时刻之间挑一个空位
				ts := timesByMeter[mid]
				a := ts[rng.Intn(len(ts))]
				b := a
				if a+1 < clock {
					b = a + 1
				}
				at = b
			}
			// 保证推进时钟（追加情形）
			engM := e.meters[mid]
			if engM != nil && at < engM.installTime {
				at = engM.installTime
			}
			if at >= math.MaxInt64 {
				continue
			}
			mod := uint64(1)
			if engM != nil {
				mod = engM.mod
			}
			value := uint64(rng.Int63n(int64(mod)))
			fmt.Fprintf(logFile, "iter=%d step=%d Register(%s t=%d v=%d kind=%d)\n", iter, step, mid, at, value, kind)
			e1 := e.RegisterReading(mid, at, value, kind)
			e2 := nm.register(mid, at, value, kind)
			if !assertSameErr(t, e1, e2) {
				t.Fatalf("iter=%d step=%d register 分歧: engine=%v naive=%v", iter, step, e1, e2)
			}
			if e1 == nil {
				clock = at
				timesByMeter[mid] = append(timesByMeter[mid], at)
				sort.Slice(timesByMeter[mid], func(a, b int) bool { return timesByMeter[mid][a] < timesByMeter[mid][b] })
			}

			// 以一定概率删除估算、换表、查询。
			switch rng.Intn(6) {
			case 0:
				ts := timesByMeter[mid]
				if len(ts) > 0 {
					tt := ts[rng.Intn(len(ts))]
					fmt.Fprintf(logFile, "iter=%d DeleteEst(%s t=%d)\n", iter, mid, tt)
					assertSameErr(t, e.DeleteEstimatedReading(mid, tt), nm.deleteEst(mid, tt))
				}
			case 1:
				// 查询：端点优先选已有读数
				if _, ok := e.points[pointID]; ok {
					all := allPointTimes(e, pointID)
					if len(all) >= 2 {
						a, b := all[rng.Intn(len(all))], all[rng.Intn(len(all))]
						if a > b {
							a, b = b, a
						}
						q1, err1 := e.QueryUsage(pointID, a, b)
						q2, err2 := nm.query(pointID, a, b)
						if !assertSameErr(t, err1, err2) {
							t.Fatalf("query 分歧 a=%d b=%d", a, b)
						}
						if err1 == nil {
							if q1.Usage.Cmp(q2.Usage) != 0 || q1.ContainsEstimated != q2.ContainsEstimated {
								t.Fatalf("query 结果分歧 [%d,%d]: engine=%v naive=%v", a, b, q1, q2)
							}
							// 三点可加性
							c := all[rng.Intn(len(all))]
							if c >= a && c <= b {
								l, _ := e.QueryUsage(pointID, a, c)
								r, _ := e.QueryUsage(pointID, c, b)
								if new(big.Int).Add(l.Usage, r.Usage).Cmp(q1.Usage) != 0 {
									t.Fatalf("可加性失败 %d..%d..%d: %s+%s != %s", a, c, b, l.Usage, r.Usage, q1.Usage)
								}
							}
						}
					}
				}
			case 2:
				// 换表
				curID, active := activeEngineMeter(e, pointID)
				if active {
					nmCur := nm.meters[curID]
					rt := nmCur.readings[len(nmCur.readings)-1].Time
					t2 := rt
					if rng.Intn(2) == 0 {
						t2 = clock + int64(rng.Intn(5))
					}
					nm2 := newMeter()
					oldLast := uint64(rng.Int63n(int64(nmCur.mod)))
					newInit := uint64(rng.Int63n(int64(nm.meters[nm2].mod)))
					// 让旧表末次值倾向等于衔接时刻已有读数，增加同刻覆盖
					if rng.Intn(2) == 0 {
						oldLast = nmCur.readings[len(nmCur.readings)-1].Value
					}
					fmt.Fprintf(logFile, "iter=%d Replace(t=%d oldLast=%d new=%s init=%d)\n", iter, t2, oldLast, nm2, newInit)
					e1 := e.ReplaceMeter(pointID, t2, oldLast, nm2, newInit)
					e2 := nm.replace(pointID, t2, oldLast, nm2, newInit)
					if !assertSameErr(t, e1, e2) {
						t.Fatalf("replace 分歧: engine=%v naive=%v", e1, e2)
					}
					if e1 == nil {
						clock = t2
						timesByMeter[curID] = append(timesByMeter[curID], t2)
						timesByMeter[nm2] = append(timesByMeter[nm2], t2)
					}
				}
			case 3:
				// 当前无活动表时，以一定概率把一只已拆除的旧表重新挂接（多 tour）。
				if _, active := activeEngineMeter(e, pointID); !active {
					var candidates []string
					for _, id := range meterIDs {
						if e.meters[id].pointID == "" {
							candidates = append(candidates, id)
						}
					}
					if len(candidates) > 0 {
						id := candidates[rng.Intn(len(candidates))]
						at := clock + 1 + int64(rng.Intn(5))
						mod := e.meters[id].mod
						init := uint64(rng.Int63n(int64(mod)))
						fmt.Fprintf(logFile, "iter=%d Reattach(%s t=%d init=%d)\n", iter, id, at, init)
						e1 := e.Attach(pointID, id, at, init)
						e2 := nm.attach(pointID, id, at, init)
						if !assertSameErr(t, e1, e2) {
							t.Fatalf("reattach 分歧: engine=%v naive=%v", e1, e2)
						}
						if e1 == nil {
							clock = at
							timesByMeter[id] = append(timesByMeter[id], at)
						}
					}
				}
			}
		}
	}
	t.Logf("随机对照完成，日志见 /tmp/ontology_differential.log")
}

func assertSameErr(t *testing.T, e1, e2 error) bool {
	t.Helper()
	code := func(err error) ErrorCode {
		if err == nil {
			return 0
		}
		var me *MeterError
		if errors.As(err, &me) {
			return me.Code
		}
		return -1
	}
	if code(e1) != code(e2) {
		t.Errorf("错误类别分歧: engine=%v naive=%v", e1, e2)
		return false
	}
	return true
}

func allPointTimes(e *Engine, point string) []int64 {
	p := e.points[point]
	var out []int64
	seen := map[int64]bool{}
	for _, seg := range p.segments {
		m := e.meters[seg.meterID]
		var walk func(n *node)
		walk = func(n *node) {
			if n == nil {
				return
			}
			walk(n.left)
			if !seen[n.key] {
				seen[n.key] = true
				out = append(out, n.key)
			}
			walk(n.right)
		}
		walk(m.tree.root)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func activeEngineMeter(e *Engine, point string) (string, bool) {
	p := e.points[point]
	if p == nil || len(p.segments) == 0 {
		return "", false
	}
	s := p.segments[len(p.segments)-1]
	if s.removeTime != math.MaxInt64 {
		return "", false
	}
	return s.meterID, true
}
