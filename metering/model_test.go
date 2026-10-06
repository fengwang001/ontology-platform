package metering

import (
	"fmt"
	"math/big"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// 本文件包含一份独立编写的朴素模型：语义与 Service 相同，但实现刻意
// 朴素（全量扫描、每次全量重算、独立的分摊实现），用于随机操作序列的
// 差分对照测试，并逐步打印输入、输出与判定依据。

type nEntry struct {
	time, value        int64
	estimate, replace  bool
	oldFinal, newStart int64
}

type nMeter struct {
	rangeMax, area int64
	entries        []nEntry
	occ            [][2]int64
}

type naive struct {
	gap      int64
	now      int64
	hasNow   bool
	masterID string
	unitIDs  []string
	meters   map[string]*nMeter
	bills    map[[2]int64]*Bill
	current  map[[2]int64]*Bill
}

func newNaive(gap int64) *naive {
	return &naive{gap: gap, meters: map[string]*nMeter{}, bills: map[[2]int64]*Bill{}, current: map[[2]int64]*Bill{}}
}

func nLeft(e nEntry) int64 {
	if e.replace {
		return e.newStart
	}
	return e.value
}

func nRight(e nEntry) int64 {
	if e.replace {
		return e.oldFinal
	}
	return e.value
}

func nUsageBetween(v1, v2, rng int64) (int64, bool) {
	if v2 >= v1 {
		return v2 - v1, true
	}
	if 2*(v1-v2) <= rng {
		return rng - v1 + v2, true
	}
	return 0, false
}

func nMulDiv(a, b, c int64) int64 {
	z := new(big.Int).Mul(big.NewInt(a), big.NewInt(b))
	return z.Quo(z, big.NewInt(c)).Int64()
}

// nUsage 全量扫描整个读数序列（朴素实现，开销随历史总量增长）。
func (m *nMeter) nUsage(s, e int64) int64 {
	var total int64
	for i := 0; i+1 < len(m.entries); i++ {
		a, b := m.entries[i], m.entries[i+1]
		lo, hi := max(s, a.time), min(e, b.time)
		if lo >= hi {
			continue
		}
		u, ok := nUsageBetween(nLeft(a), nRight(b), m.rangeMax)
		if !ok {
			continue
		}
		d := b.time - a.time
		total += nMulDiv(u, hi-a.time, d) - nMulDiv(u, lo-a.time, d)
	}
	return total
}

// nOccupied 朴素地归并在住区间后求与 [s,e) 的重叠时长。
func (m *nMeter) nOccupied(s, e int64) (int64, bool) {
	ivs := append([][2]int64(nil), m.occ...)
	sort.Slice(ivs, func(i, j int) bool { return ivs[i][0] < ivs[j][0] })
	var total int64
	has := false
	var cur [2]int64
	for i, iv := range ivs {
		if i == 0 || iv[0] > cur[1] {
			cur = iv
		} else {
			cur[1] = max(cur[1], iv[1])
		}
		if i+1 < len(ivs) && ivs[i+1][0] <= cur[1] {
			continue
		}
		lo, hi := max(s, cur[0]), min(e, cur[1])
		if lo < hi {
			has = true
			total += hi - lo
		}
	}
	return total, has
}

// nAllocate 独立实现的最大余数分摊：显式按（余数降序、户号升序）排序。
func nAllocate(total int64, weights []int64) []int64 {
	out := make([]int64, len(weights))
	if total == 0 || len(weights) == 0 {
		return out
	}
	if total < 0 {
		neg := nAllocate(-total, weights)
		for i := range neg {
			neg[i] = -neg[i]
		}
		return neg
	}
	var wSum int64
	for _, w := range weights {
		wSum += w
	}
	if wSum <= 0 {
		return out
	}
	type frac struct {
		idx int
		rem *big.Int
	}
	bigW := big.NewInt(wSum)
	fracs := make([]frac, len(weights))
	var floorSum int64
	for i, w := range weights {
		prod := new(big.Int).Mul(big.NewInt(total), big.NewInt(w))
		q := new(big.Int).Quo(prod, bigW)
		out[i] = q.Int64()
		floorSum += q.Int64()
		rem := new(big.Int).Sub(prod, new(big.Int).Mul(q, bigW))
		fracs[i] = frac{idx: i, rem: rem}
	}
	sort.Slice(fracs, func(a, b int) bool {
		if c := fracs[a].rem.Cmp(fracs[b].rem); c != 0 {
			return c > 0
		}
		return fracs[a].idx < fracs[b].idx
	})
	for k := int64(0); k < total-floorSum; k++ {
		out[fracs[k].idx]++
	}
	return out
}

// nBill 朴素地重算一个账期的账单（允许负公摊，供更正重算）。
func (n *naive) nBill(s, e int64) *Bill {
	master := n.meters[n.masterID].nUsage(s, e)
	owns := make([]int64, len(n.unitIDs))
	weights := make([]int64, len(n.unitIDs))
	var ownSum int64
	for i, id := range n.unitIDs {
		u := n.meters[id]
		owns[i] = u.nUsage(s, e)
		ownSum += owns[i]
		occ, has := u.nOccupied(s, e)
		if !has {
			occ = e - s
		}
		weights[i] = u.area * occ
	}
	shared := master - ownSum
	shares := nAllocate(shared, weights)
	b := &Bill{Start: s, End: e, MasterUsage: master, SharedUsage: shared}
	for i, id := range n.unitIDs {
		b.Units = append(b.Units, UnitBill{UnitID: id, Own: owns[i], Share: shares[i], Payable: shares[i]})
	}
	return b
}
func (n *naive) clock(now int64) (ErrCode, string) {
	if n.hasNow && now < n.now {
		return ErrClockRollback, fmt.Sprintf("now=%d < 上次 now=%d", now, n.now)
	}
	return 0, ""
}

func (n *naive) addMeter(now int64, id string, role Role, rng, area int64) (ErrCode, string) {
	if id == "" || rng <= 0 || (role != Master && role != Unit) ||
		(role == Master && area != 0) || (role == Unit && area <= 0) {
		return ErrInvalidParam, "表参数非法"
	}
	if c, msg := n.clock(now); c != 0 {
		return c, msg
	}
	if _, dup := n.meters[id]; dup {
		return ErrInvalidParam, "表已存在"
	}
	if role == Master && n.masterID != "" {
		return ErrInvalidParam, "总表已存在"
	}
	n.meters[id] = &nMeter{rangeMax: rng, area: area}
	if role == Master {
		n.masterID = id
	} else {
		n.unitIDs = append(n.unitIDs, id)
		sort.Strings(n.unitIDs)
	}
	n.now, n.hasNow = now, true
	return 0, ""
}

func (n *naive) addOccupancy(now int64, id string, s, e int64) (ErrCode, string) {
	if s < 0 || s >= e {
		return ErrInvalidParam, "在住区间非法"
	}
	if c, msg := n.clock(now); c != 0 {
		return c, msg
	}
	m, ok := n.meters[id]
	if !ok || id == n.masterID {
		return ErrMeterNotFound, "分户表不存在"
	}
	m.occ = append(m.occ, [2]int64{s, e})
	n.now, n.hasNow = now, true
	return 0, ""
}

func nLastActual(es []nEntry) int {
	for i := len(es) - 1; i >= 0; i-- {
		if !es[i].estimate {
			return i
		}
	}
	return -1
}

func (n *naive) record(now int64, id string, t, v int64, kind Kind) ([]Correction, ErrCode, string) {
	if id == "" || t < 0 || v < 0 || t > now || (kind != Actual && kind != Estimate) {
		return nil, ErrInvalidParam, "读数参数非法"
	}
	if c, msg := n.clock(now); c != 0 {
		return nil, c, msg
	}
	m, ok := n.meters[id]
	if !ok {
		return nil, ErrMeterNotFound, "表不存在"
	}
	if v > m.rangeMax {
		return nil, ErrInvalidParam, "读数超出量程"
	}
	if len(m.entries) > 0 && t <= m.entries[len(m.entries)-1].time {
		return nil, ErrOutOfOrder, fmt.Sprintf("t=%d 不晚于 %d", t, m.entries[len(m.entries)-1].time)
	}
	if kind == Estimate {
		la := nLastActual(m.entries)
		if la < 0 {
			return nil, ErrEstimateNotAllowed, "无实抄基线"
		}
		if t-m.entries[la].time <= n.gap {
			return nil, ErrEstimateNotAllowed, fmt.Sprintf("距实抄 %d 未超过 %d", t-m.entries[la].time, n.gap)
		}
		if v < nLeft(m.entries[len(m.entries)-1]) {
			return nil, ErrEstimateNotAllowed, "估抄值小于上一次读数"
		}
		m.entries = append(m.entries, nEntry{time: t, value: v, estimate: true})
	} else {
		la := nLastActual(m.entries)
		base := m.entries[:la+1]
		if la >= 0 {
			if _, ok := nUsageBetween(nLeft(base[la]), v, m.rangeMax); !ok {
				return nil, ErrIllegalReading, fmt.Sprintf("自 %d 跌至 %d 超过量程一半", nLeft(base[la]), v)
			}
		}
		ne := make([]nEntry, 0, len(base)+1)
		ne = append(ne, base...)
		ne = append(ne, nEntry{time: t, value: v})
		m.entries = ne
	}
	n.now, n.hasNow = now, true
	return n.recalc(), 0, ""
}

func (n *naive) replace(now int64, id string, t, oldFinal, newStart int64) ([]Correction, ErrCode, string) {
	if id == "" || t < 0 || oldFinal < 0 || newStart < 0 || t > now {
		return nil, ErrInvalidParam, "换表参数非法"
	}
	if c, msg := n.clock(now); c != 0 {
		return nil, c, msg
	}
	m, ok := n.meters[id]
	if !ok {
		return nil, ErrMeterNotFound, "表不存在"
	}
	if oldFinal > m.rangeMax || newStart > m.rangeMax {
		return nil, ErrInvalidParam, "换表读数超出量程"
	}
	if len(m.entries) > 0 && t <= m.entries[len(m.entries)-1].time {
		return nil, ErrOutOfOrder, "换表时刻乱序"
	}
	la := nLastActual(m.entries)
	base := m.entries[:la+1]
	if la >= 0 {
		if _, ok := nUsageBetween(nLeft(base[la]), oldFinal, m.rangeMax); !ok {
			return nil, ErrIllegalReading, "旧表终读数跌落超过量程一半"
		}
	}
	ne := make([]nEntry, 0, len(base)+1)
	ne = append(ne, base...)
	ne = append(ne, nEntry{time: t, replace: true, oldFinal: oldFinal, newStart: newStart})
	m.entries = ne
	n.now, n.hasNow = now, true
	return n.recalc(), 0, ""
}

func (n *naive) settle(now, s, e int64) (*Bill, ErrCode, string) {
	if s < 0 || s >= e || e > now || n.masterID == "" {
		return nil, ErrInvalidParam, "账期参数非法"
	}
	if c, msg := n.clock(now); c != 0 {
		return nil, c, msg
	}
	key := [2]int64{s, e}
	if _, dup := n.bills[key]; dup {
		return nil, ErrPeriodSettled, "账期已结算"
	}
	b := n.nBill(s, e)
	if b.SharedUsage < 0 {
		return nil, ErrNegativeShared, fmt.Sprintf("总表 %d < 分户合计", b.MasterUsage)
	}
	n.bills[key] = b
	n.current[key] = b
	n.now, n.hasNow = now, true
	return b, 0, ""
}

// recalc 朴素地重算全部已结算账期，为公摊变化者生成更正。
func (n *naive) recalc() []Correction {
	keys := make([][2]int64, 0, len(n.bills))
	for k := range n.bills {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})
	var out []Correction
	for _, k := range keys {
		old := n.current[k]
		nb := n.nBill(k[0], k[1])
		sharedDelta := nb.SharedUsage - old.SharedUsage
		if sharedDelta == 0 {
			continue
		}
		oldShare := map[string]int64{}
		for _, u := range old.Units {
			oldShare[u.UnitID] = u.Share
		}
		var deltas []UnitDelta
		for _, u := range nb.Units {
			if d := u.Share - oldShare[u.UnitID]; d != 0 {
				deltas = append(deltas, UnitDelta{UnitID: u.UnitID, Delta: d})
			}
		}
		out = append(out, Correction{Start: k[0], End: k[1], SharedDelta: sharedDelta, Deltas: deltas})
		n.current[k] = nb
	}
	return out
}

func corrEq(a, b []Correction) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

func checkBillInvariants(t *testing.T, b *Bill) {
	t.Helper()
	var ownSum, shareSum int64
	for _, u := range b.Units {
		ownSum += u.Own
		shareSum += u.Share
		if u.Payable != u.Share {
			t.Fatalf("应付 %d != 公摊分摊 %d", u.Payable, u.Share)
		}
	}
	if shareSum != b.SharedUsage {
		t.Fatalf("分摊之和 %d != 公摊 %d", shareSum, b.SharedUsage)
	}
	if ownSum+shareSum != b.MasterUsage {
		t.Fatalf("不守恒: 自用 %d + 分摊 %d != 总表 %d", ownSum, shareSum, b.MasterUsage)
	}
}

func checkCorrInvariants(t *testing.T, cs []Correction) {
	t.Helper()
	for _, c := range cs {
		var sum int64
		for _, d := range c.Deltas {
			sum += d.Delta
		}
		if sum != c.SharedDelta {
			t.Fatalf("[%d,%d) 各户差额之和 %d != 公摊差额 %d", c.Start, c.End, sum, c.SharedDelta)
		}
	}
}

// TestDifferentialRandom 以随机操作序列对照 Service 与朴素模型，
// 每步打印输入、输出与判定依据。
func TestDifferentialRandom(t *testing.T) {
	for seed := int64(0); seed < 30; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			r := rand.New(rand.NewSource(seed))
			svc := New(Config{Gap: 5})
			nv := newNaive(5)
			ranges := map[string]int64{"M": 1000, "U1": 500, "U2": 800, "U3": 1000}
			areas := map[string]int64{"U1": 10, "U2": 20, "U3": 30}
			if err := svc.AddMeter(0, "M", Master, 1000, 0); err != nil {
				t.Fatal(err)
			}
			if c, msg := nv.addMeter(0, "M", Master, 1000, 0); c != 0 {
				t.Fatalf("%v: %s", c, msg)
			}
			for _, id := range []string{"U1", "U2", "U3"} {
				if err := svc.AddMeter(0, id, Unit, ranges[id], areas[id]); err != nil {
					t.Fatal(err)
				}
				if c, msg := nv.addMeter(0, id, Unit, ranges[id], areas[id]); c != 0 {
					t.Fatalf("%v: %s", c, msg)
				}
			}
			ids := []string{"M", "U1", "U2", "U3"}
			units := []string{"U1", "U2", "U3"}
			lastT := map[string]int64{}
			var now int64
			for step := 0; step < 300; step++ {
				op := r.Intn(100)
				switch {
				case op < 38 || op < 52: // 实抄 / 估抄
					kind := Actual
					if op >= 38 {
						kind = Estimate
					}
					id := ids[r.Intn(len(ids))]
					tt := lastT[id] + 1 + int64(r.Intn(30))
					now = max(now, tt)
					v := int64(r.Intn(int(ranges[id]) + 1))
					sc, serr := svc.Record(now, id, tt, v, kind)
					nc, nc2, nmsg := nv.record(now, id, tt, v, kind)
					if codeOf(t, serr) != nc2 {
						t.Fatalf("step %d Record(%s,%s t=%d v=%d): 错误不一致 %v vs %v",
							step, id, map[Kind]string{Actual: "实抄", Estimate: "估抄"}[kind], tt, v, serr, nc2)
					}
					if !corrEq(sc, nc) {
						t.Fatalf("step %d 更正不一致:\n%+v\n%+v", step, sc, nc)
					}
					checkCorrInvariants(t, sc)
					if serr == nil {
						lastT[id] = tt
					}
					t.Logf("step %03d 录入%s id=%s t=%d v=%d now=%d -> code=%v 更正=%d 依据=%s",
						step, map[Kind]string{Actual: "实抄", Estimate: "估抄"}[kind],
						id, tt, v, now, codeOf(t, serr), len(sc), nmsg)
				case op < 62: // 换表
					id := ids[r.Intn(len(ids))]
					tt := lastT[id] + 1 + int64(r.Intn(20))
					now = max(now, tt)
					oldF := int64(r.Intn(int(ranges[id]) + 1))
					newS := int64(r.Intn(int(ranges[id]) + 1))
					sc, serr := svc.ReplaceMeter(now, id, tt, oldF, newS)
					nc, nc2, nmsg := nv.replace(now, id, tt, oldF, newS)
					if codeOf(t, serr) != nc2 {
						t.Fatalf("step %d Replace(%s t=%d %d->%d): 错误不一致 %v vs %v", step, id, tt, oldF, newS, serr, nc2)
					}
					if !corrEq(sc, nc) {
						t.Fatalf("step %d 更正不一致:\n%+v\n%+v", step, sc, nc)
					}
					checkCorrInvariants(t, sc)
					if serr == nil {
						lastT[id] = tt
					}
					t.Logf("step %03d 换表 id=%s t=%d 旧终=%d 新始=%d now=%d -> code=%v 更正=%d 依据=%s",
						step, id, tt, oldF, newS, now, codeOf(t, serr), len(sc), nmsg)
				case op < 70: // 在住登记
					id := units[r.Intn(len(units))]
					s0 := int64(r.Intn(int(now) + 1))
					e0 := s0 + 1 + int64(r.Intn(20))
					serr := svc.AddOccupancy(now, id, s0, e0)
					nc2, nmsg := nv.addOccupancy(now, id, s0, e0)
					if codeOf(t, serr) != nc2 {
						t.Fatalf("step %d Occupancy(%s [%d,%d)): 错误不一致 %v vs %v", step, id, s0, e0, serr, nc2)
					}
					t.Logf("step %03d 在住 id=%s [%d,%d) now=%d -> code=%v 依据=%s", step, id, s0, e0, now, codeOf(t, serr), nmsg)
				case op < 76: // 乱序录入（错误路径对照）
					id := ids[r.Intn(len(ids))]
					tt := lastT[id] - int64(r.Intn(3))
					v := int64(r.Intn(int(ranges[id]) + 1))
					_, serr := svc.Record(now, id, tt, v, Actual)
					_, nc2, nmsg := nv.record(now, id, tt, v, Actual)
					if codeOf(t, serr) != nc2 {
						t.Fatalf("step %d 乱序(%s t=%d): 错误不一致 %v vs %v", step, id, tt, serr, nc2)
					}
					t.Logf("step %03d 乱序 id=%s t=%d now=%d -> code=%v 依据=%s", step, id, tt, now, codeOf(t, serr), nmsg)
				case op < 80: // 时钟回退（错误路径对照）
					bad := now - 1 - int64(r.Intn(3))
					_, serr := svc.Record(bad, "M", 0, 0, Actual)
					_, nc2, nmsg := nv.record(bad, "M", 0, 0, Actual)
					if codeOf(t, serr) != nc2 {
						t.Fatalf("step %d 回退: 错误不一致 %v vs %v", step, serr, nc2)
					}
					t.Logf("step %03d 回退 now=%d -> code=%v 依据=%s", step, bad, codeOf(t, serr), nmsg)
				default: // 结算
					s0 := int64(r.Intn(int(now) + 1))
					e0 := s0 + 1 + int64(r.Intn(40))
					if e0 > now {
						e0 = now
					}
					if s0 >= e0 {
						continue
					}
					sb, serr := svc.Settle(now, s0, e0)
					nb, nc2, nmsg := nv.settle(now, s0, e0)
					if codeOf(t, serr) != nc2 {
						t.Fatalf("step %d Settle([%d,%d)): 错误不一致 %v vs %v", step, s0, e0, serr, nc2)
					}
					if serr == nil {
						if !reflect.DeepEqual(sb, nb) {
							t.Fatalf("step %d 账单不一致:\n%+v\n%+v", step, sb, nb)
						}
						checkBillInvariants(t, sb)
					}
					if serr == nil {
						t.Logf("step %03d 结算 [%d,%d) now=%d -> code=0 总表=%d 公摊=%d 各户=%v",
							step, s0, e0, now, sb.MasterUsage, sb.SharedUsage, sb.Units)
					} else {
						t.Logf("step %03d 结算 [%d,%d) now=%d -> code=%v 依据=%s",
							step, s0, e0, now, codeOf(t, serr), nmsg)
					}
				}
			}
		})
	}
}
