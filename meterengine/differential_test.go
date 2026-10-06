package meterengine

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// naiveModel 是独立于引擎实现的朴素参考模型：
// 每只电表用有序切片保存全部读数，所有判定都线性重扫。
type naiveModel struct {
	log      *strings.Builder
	digits   map[string]int
	mult     map[string]int64
	modulus  map[string]int64
	readings map[string][]naiveReading
	attached map[string]naiveAttach
	points   map[string][]string
	limit    map[string]int64
}

type naiveReading struct {
	time    int64
	display int64
	kind    ReadingType
}

type naiveAttach struct {
	point   string
	install int64
	remove  int64
}

func newNaive(log *strings.Builder) *naiveModel {
	return &naiveModel{
		log:      log,
		digits:   map[string]int{},
		mult:     map[string]int64{},
		modulus:  map[string]int64{},
		readings: map[string][]naiveReading{},
		attached: map[string]naiveAttach{},
		points:   map[string][]string{},
		limit:    map[string]int64{},
	}
}

func (n *naiveModel) verdict(err error) {
	if err == nil {
		fmt.Fprint(n.log, "  -> OK\n")
	} else {
		fmt.Fprintf(n.log, "  -> 拒绝[%d:%v]\n", errClass(err), err)
	}
}

func (n *naiveModel) registerMeter(id string, digits int, mult int64) error {
	fmt.Fprintf(n.log, "registerMeter(%s,%d,%d)", id, digits, mult)
	var err error
	switch {
	case id == "":
		err = errInvalid("空")
	case digits <= 0 || digits > 18 || mult <= 0:
		err = errInvalid("位数/倍率")
	default:
		var mod int64 = 1
		for i := 0; i < digits; i++ {
			mod *= 10
		}
		if _, ok := n.digits[id]; ok {
			err = errConflict("重复登记")
		} else {
			n.digits[id] = digits
			n.mult[id] = mult
			n.modulus[id] = mod
		}
	}
	n.verdict(err)
	return err
}

func (n *naiveModel) sorted(meter string) []naiveReading {
	rs := append([]naiveReading(nil), n.readings[meter]...)
	sort.Slice(rs, func(i, j int) bool { return rs[i].time < rs[j].time })
	return rs
}

func findIdx(rs []naiveReading, t int64) int {
	for i, r := range rs {
		if r.time == t {
			return i
		}
	}
	return -1
}

func (n *naiveModel) delta(meter string, a, b naiveReading) (int64, int) {
	if b.display >= a.display {
		return b.display - a.display, 0
	}
	return b.display + n.modulus[meter] - a.display, 1
}

func (n *naiveModel) segmentOK(point, meter string, a, b naiveReading) bool {
	d, _ := n.delta(meter, a, b)
	used := d * n.mult[meter]
	lim := n.limit[point]
	if lim <= 0 {
		return true
	}
	return used <= lim*(b.time-a.time)
}

func (n *naiveModel) install(point, meter string, at int64, init int64) error {
	fmt.Fprintf(n.log, "install(%s,%s,t=%d,init=%d)", point, meter, at, init)
	var err error
	switch {
	case point == "" || meter == "" || at <= 0:
		err = errInvalid("参数")
	case n.digits[meter] == 0:
		err = errInvalid("未登记电表")
	case init < 0 || init > n.modulus[meter]-1:
		err = errInvalid("显示值")
	case len(n.points[point]) > 0:
		err = errConflict("供电点已有挂接")
	default:
		if _, used := n.attached[meter]; used {
			err = errConflict("电表已挂接过")
		} else {
			n.attached[meter] = naiveAttach{point: point, install: at}
			n.points[point] = append(n.points[point], meter)
			n.readings[meter] = append(n.readings[meter], naiveReading{at, init, Actual})
		}
	}
	n.verdict(err)
	return err
}

func (n *naiveModel) reading(meter string, at int64, display int64, kind ReadingType) error {
	label := "实抄"
	if kind == Estimated {
		label = "估算"
	}
	fmt.Fprintf(n.log, "reading(%s,t=%d,v=%d,%s)", meter, at, display, label)
	var err error
	switch {
	case meter == "" || at <= 0 || (kind != Actual && kind != Estimated):
		err = errInvalid("参数")
	case n.digits[meter] == 0:
		err = errInvalid("未登记电表")
	case display < 0 || display > n.modulus[meter]-1:
		err = errInvalid("显示值")
	default:
		a, ok := n.attached[meter]
		if !ok || at < a.install || (a.remove != 0 && at >= a.remove) {
			err = errNotAttached("不在挂接期")
		} else {
			err = n.readingImpl(a.point, meter, at, display, kind)
		}
	}
	n.verdict(err)
	return err
}

func (n *naiveModel) rawIndex(meter string, t int64) int {
	return findIdx(n.readings[meter], t)
}

func (n *naiveModel) readingImpl(point, meter string, at int64, display int64, kind ReadingType) error {
	rs := n.sorted(meter)
	idx := findIdx(rs, at)
	if idx >= 0 {
		old := rs[idx]
		switch {
		case old.kind == Actual && kind == Actual:
			if old.display != display {
				return errReadingConflict("实抄冲突")
			}
			return nil
		case old.kind == Estimated && kind == Estimated:
			return errReadingConflict("估算冲突")
		}
		cand := naiveReading{at, display, Actual}
		if idx > 0 && !n.segmentOK(point, meter, rs[idx-1], cand) {
			return errUnreasonable("替换后前区间")
		}
		if idx+1 < len(rs) && !n.segmentOK(point, meter, cand, rs[idx+1]) {
			return errUnreasonable("替换后后区间")
		}
		n.readings[meter][n.rawIndex(meter, at)] = cand
		return nil
	}
	cand := naiveReading{at, display, kind}
	if len(rs) > 0 && at < rs[len(rs)-1].time {
		if kind == Estimated {
			return errConflict("估算乱序")
		}
		pos := sort.Search(len(rs), func(i int) bool { return rs[i].time > at })
		pred, succ := rs[pos-1], rs[pos]
		_, oldFlips := n.delta(meter, pred, succ)
		_, f1 := n.delta(meter, pred, cand)
		_, f2 := n.delta(meter, cand, succ)
		if f1+f2 != oldFlips {
			return errConflict("翻转不守恒")
		}
		if !n.segmentOK(point, meter, pred, cand) || !n.segmentOK(point, meter, cand, succ) {
			return errUnreasonable("拆分区间")
		}
		n.readings[meter] = append(n.readings[meter], cand)
		return nil
	}
	if len(rs) > 0 && !n.segmentOK(point, meter, rs[len(rs)-1], cand) {
		return errUnreasonable("追加区间")
	}
	n.readings[meter] = append(n.readings[meter], cand)
	return nil
}

func (n *naiveModel) swap(point string, at int64, oldLast int64, newMeter string, newInit int64) error {
	fmt.Fprintf(n.log, "swap(%s,t=%d,oldLast=%d,new=%s,newInit=%d)", point, at, oldLast, newMeter, newInit)
	var err error
	switch {
	case point == "" || newMeter == "" || at <= 0:
		err = errInvalid("参数")
	case len(n.points[point]) == 0:
		err = errNotAttached("无在装表")
	case n.digits[newMeter] == 0:
		err = errInvalid("新表未登记")
	}
	if err == nil {
		err = n.swapImpl(point, at, oldLast, newMeter, newInit)
	}
	n.verdict(err)
	return err
}

func (n *naiveModel) swapImpl(point string, at int64, oldLast int64, newMeter string, newInit int64) error {
	old := n.points[point][len(n.points[point])-1]
	if oldLast < 0 || oldLast > n.modulus[old]-1 || newInit < 0 || newInit > n.modulus[newMeter]-1 {
		return errInvalid("显示值超位")
	}
	if _, used := n.attached[newMeter]; used {
		return errConflict("新表已挂接过")
	}
	rs := n.sorted(old)
	latest := rs[len(rs)-1]
	if at < latest.time {
		return errConflict("早于最新读数")
	}
	if idx := findIdx(rs, at); idx >= 0 {
		exist := rs[idx]
		if exist.kind == Actual {
			if exist.display != oldLast {
				return errReadingConflict("末次不一致")
			}
		} else {
			cand := naiveReading{at, oldLast, Actual}
			if idx > 0 && !n.segmentOK(point, old, rs[idx-1], cand) {
				return errUnreasonable("旧表替换")
			}
		}
	} else if !n.segmentOK(point, old, latest, naiveReading{at, oldLast, Actual}) {
		return errUnreasonable("旧表末次区间")
	}
	if ri := n.rawIndex(old, at); ri >= 0 {
		n.readings[old][ri] = naiveReading{at, oldLast, Actual}
	} else {
		n.readings[old] = append(n.readings[old], naiveReading{at, oldLast, Actual})
	}
	a := n.attached[old]
	a.remove = at
	n.attached[old] = a
	n.attached[newMeter] = naiveAttach{point: point, install: at}
	n.points[point] = append(n.points[point], newMeter)
	n.readings[newMeter] = append(n.readings[newMeter], naiveReading{at, newInit, Actual})
	return nil
}

func (n *naiveModel) deleteEst(meter string, at int64) error {
	fmt.Fprintf(n.log, "deleteEst(%s,t=%d)", meter, at)
	var err error
	switch {
	case meter == "" || at <= 0:
		err = errInvalid("参数")
	case n.digits[meter] == 0:
		err = errInvalid("未登记")
	default:
		a, ok := n.attached[meter]
		if !ok {
			err = errNotAttached("无挂接")
		} else {
			rs := n.sorted(meter)
			idx := findIdx(rs, at)
			switch {
			case idx < 0:
				err = errNoReading("无读数")
			case rs[idx].kind == Actual:
				err = errConflict("实抄不可删")
			case idx > 0 && idx+1 < len(rs) &&
				!n.segmentOK(a.point, meter, rs[idx-1], rs[idx+1]):
				err = errUnreasonable("合并区间")
			default:
				ri := n.rawIndex(meter, at)
				n.readings[meter] = append(n.readings[meter][:ri], n.readings[meter][ri+1:]...)
			}
		}
	}
	n.verdict(err)
	return err
}

// query 按朴素方式沿挂接顺序拼接区间求和。
func (n *naiveModel) query(point string, from, to int64) (QueryResult, error) {
	fmt.Fprintf(n.log, "query(%s,%d,%d)", point, from, to)
	if point == "" || from <= 0 || to <= 0 || from > to {
		err := errInvalid("参数")
		n.verdict(err)
		return QueryResult{}, err
	}
	meters := n.points[point]
	if len(meters) == 0 {
		err := errNoReading("无挂接")
		n.verdict(err)
		return QueryResult{}, err
	}
	res := QueryResult{}
	foundFrom, foundTo := false, false
	for _, meter := range meters {
		a := n.attached[meter]
		rs := n.sorted(meter)
		var chain []naiveReading
		for _, r := range rs {
			if r.time < from || r.time > to {
				continue
			}
			if r.time < a.install || (a.remove != 0 && r.time > a.remove) {
				continue
			}
			chain = append(chain, r)
		}
		for i := range chain {
			if chain[i].kind == Estimated {
				res.HasEstimated = true
			}
			if chain[i].time == from {
				foundFrom = true
			}
			if chain[i].time == to {
				foundTo = true
			}
			if i > 0 {
				d, _ := n.delta(meter, chain[i-1], chain[i])
				res.Energy += d * n.mult[meter]
				if chain[i-1].kind == Estimated || chain[i].kind == Estimated {
					res.HasEstimated = true
				}
			}
		}
	}
	if !foundFrom || !foundTo {
		err := errNoReading("端点不是已有读数时刻")
		n.verdict(err)
		return QueryResult{}, err
	}
	n.verdict(nil)
	return res, nil
}

func sameClass(a, b error) bool {
	return errClass(a) == errClass(b)
}

// TestDifferentialRandom 对大量随机操作序列做引擎与朴素模型差分，
// 日志逐条打印输入、输出（OK/拒绝类别），分歧时输出完整判定轨迹。
func TestDifferentialRandom(t *testing.T) {
	const trials = 2000
	const opsPerTrial = 100
	const maxDigits = 3

	for trial := 0; trial < trials; trial++ {
		rng := rand.New(rand.NewSource(int64(trial*7919 + 1)))
		var log strings.Builder
		fmt.Fprintf(&log, "=== trial %d ===\n", trial)
		eng := NewEngine()
		nav := newNaive(&log)

		point := "p"
		limitChoices := []int64{0, 30, 60, 100, 200}
		lim := limitChoices[rng.Intn(len(limitChoices))]
		_ = eng.SetPointLimit(point, lim)
		nav.limit[point] = lim

		// 登记 2~4 只电表。
		meterIDs := []string{}
		nMeters := 2 + rng.Intn(3)
		for i := 0; i < nMeters; i++ {
			id := fmt.Sprintf("m%d", i)
			digits := 1 + rng.Intn(maxDigits)
			mult := int64(1 + rng.Intn(3))
			e1 := eng.RegisterMeter(id, digits, mult)
			e2 := nav.registerMeter(id, digits, mult)
			if !sameClass(e1, e2) {
				t.Fatalf("trial %d registerMeter 分歧: %v vs %v\n%s", trial, e1, e2, log.String())
			}
			if e1 == nil {
				meterIDs = append(meterIDs, id)
			}
		}

		currentMeter := meterIDs[0]
		nextMeter := 1
		installAt := int64(1 + rng.Intn(3))
		initDisp := int64(rng.Intn(int(nav.modulus[currentMeter])))
		e1 := eng.InstallMeter(point, currentMeter, installAt, initDisp)
		e2 := nav.install(point, currentMeter, installAt, initDisp)
		if !sameClass(e1, e2) {
			t.Fatalf("trial %d install 分歧: %v vs %v\n%s", trial, e1, e2, log.String())
		}

		// 已登记的时刻集合，供查询挑选真实端点。
		times := []int64{installAt}
		lastTime := installAt

		for op := 0; op < opsPerTrial; op++ {
			roll := rng.Intn(100)
			switch {
			case roll < 55: // 追加/乱序读数
				meter := currentMeter
				if rng.Intn(3) == 0 && len(meterIDs) > 1 {
					meter = meterIDs[rng.Intn(len(meterIDs))] // 有意挑不在装表触发挂接期错误
				}
				var at int64
				if rng.Intn(4) == 0 && lastTime > installAt+1 {
					at = installAt + 1 + rng.Int63n(lastTime-installAt) // 乱序
				} else {
					at = lastTime + 1 + int64(rng.Intn(4))
				}
				display := int64(rng.Intn(int(nav.modulus[meter])))
				kind := Actual
				if rng.Intn(3) == 0 {
					kind = Estimated
				}
				er1 := eng.RegisterReading(meter, at, display, kind)
				er2 := nav.reading(meter, at, display, kind)
				if !sameClass(er1, er2) {
					t.Fatalf("trial %d reading 分歧: %v vs %v\n%s", trial, er1, er2, log.String())
				}
				if er1 == nil && meter == currentMeter && at > lastTime {
					lastTime = at
				}
				if er1 == nil {
					times = append(times, at)
				}
			case roll < 65 && nextMeter < len(meterIDs): // 换表
				newMeter := meterIDs[nextMeter]
				at := lastTime
				if rng.Intn(3) != 0 {
					at = lastTime + 1 + int64(rng.Intn(3))
				}
				oldLast := int64(rng.Intn(int(nav.modulus[currentMeter])))
				newInit := int64(rng.Intn(int(nav.modulus[newMeter])))
				er1 := eng.SwapMeter(point, at, oldLast, newMeter, newInit)
				er2 := nav.swap(point, at, oldLast, newMeter, newInit)
				if !sameClass(er1, er2) {
					t.Fatalf("trial %d swap 分歧: %v vs %v\n%s", trial, er1, er2, log.String())
				}
				if er1 == nil {
					currentMeter = newMeter
					nextMeter++
					lastTime = at
					times = append(times, at)
				}
			case roll < 75: // 估算替换/删除
				if len(times) > 1 {
					tPick := times[1+rng.Intn(len(times)-1)]
					if rng.Intn(2) == 0 {
						display := int64(rng.Intn(int(nav.modulus[currentMeter])))
						er1 := eng.ReplaceEstimated(currentMeter, tPick, display)
						er2 := nav.reading(currentMeter, tPick, display, Actual)
						if !sameClass(er1, er2) {
							t.Fatalf("trial %d replace 分歧: %v vs %v\n%s", trial, er1, er2, log.String())
						}
					} else {
						er1 := eng.DeleteEstimated(currentMeter, tPick)
						er2 := nav.deleteEst(currentMeter, tPick)
						if !sameClass(er1, er2) {
							t.Fatalf("trial %d delete 分歧: %v vs %v\n%s", trial, er1, er2, log.String())
						}
					}
				}
			default: // 查询
				if len(times) >= 2 {
					i := rng.Intn(len(times))
					j := rng.Intn(len(times))
					from, to := times[i], times[j]
					if from > to {
						from, to = to, from
					}
					r1, q1 := eng.Query(point, from, to)
					r2, q2 := nav.query(point, from, to)
					if !sameClass(q1, q2) {
						t.Fatalf("trial %d query 错误分歧: %v vs %v\n%s", trial, q1, q2, log.String())
					}
					if q1 == nil && (r1.Energy != r2.Energy || r1.HasEstimated != r2.HasEstimated) {
						t.Fatalf("trial %d query 结果分歧: %+v vs %+v\n%s", trial, r1, r2, log.String())
					}
				}
			}
		}

		// 序列结束后对全部时刻对做可加性终检。
		allTimes := append([]int64(nil), times...)
		sort.Slice(allTimes, func(i, j int) bool { return allTimes[i] < allTimes[j] })
		for i := 1; i < len(allTimes); i++ {
			if allTimes[i] == allTimes[i-1] {
				continue
			}
			r01, e01 := eng.Query(point, allTimes[0], allTimes[i])
			rPrev, ePrev := eng.Query(point, allTimes[0], allTimes[i-1])
			rSeg, eSeg := eng.Query(point, allTimes[i-1], allTimes[i])
			if e01 == nil && ePrev == nil && eSeg == nil {
				if r01.Energy != rPrev.Energy+rSeg.Energy {
					t.Fatalf("trial %d 三点可加性失败: %d != %d+%d\n%s",
						trial, r01.Energy, rPrev.Energy, rSeg.Energy, log.String())
				}
			}
		}

		// 抽样打印若干条完整日志，便于人工核查判定依据。
		if trial < 3 {
			t.Logf("\n%s", log.String())
		}
	}
}
