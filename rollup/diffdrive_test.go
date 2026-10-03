package rollup

import (
	"errors"
	"math"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"

	"ontology/hold"
	"ontology/tier"
)

func newRng(seed int) *rand.Rand { return rand.New(rand.NewSource(int64(seed)*7919 + 3)) }
func holdID(i int) string        { return "h" + string(rune('A'+i%26)) + string(rune('0'+(i/26)%10)) }
func sameKind(e ...error) error {
	for i := 1; i < len(e); i++ {
		if !errors.Is(e[i], e[0]) || !errors.Is(e[0], e[i]) {
			return e[i]
		}
	}
	return nil
}

// stateEqual：L0 按 (ts,v) 排序后比较，规避插入顺序差异。
func stateEqual(a state, n *naive) bool {
	pa, pn := append([]tier.Point{}, a.l0...), append([]tier.Point{}, n.l0...)
	less := func(p []tier.Point, i, j int) bool {
		return p[i].TS < p[j].TS || (p[i].TS == p[j].TS && p[i].V < p[j].V)
	}
	sort.Slice(pa, func(i, j int) bool { return less(pa, i, j) })
	sort.Slice(pn, func(i, j int) bool { return less(pn, i, j) })
	conv := func(m map[int64]simB) map[int64]tier.Bucket {
		out := map[int64]tier.Bucket{}
		for k, b := range m {
			out[k] = tier.Bucket{Count: b.c, Sum: b.s, Min: b.mn, Max: b.mx}
		}
		return out
	}
	return reflect.DeepEqual(pa, pn) && reflect.DeepEqual(a.l1, conv(n.l1)) &&
		reflect.DeepEqual(a.l2, conv(n.l2))
}

// 生产 Store / 朴素直进 / 朴素分段推进 三流逐位对照。
// 分段流对每次 Advance 插入中间时刻，验证 Advance(t1);Advance(t2) == Advance(t2)。
func TestDifferentialNaive(t *testing.T) {
	cfgs := [][4]int64{
		{120000, 7_200_000, 86_400_000, 8},
		{60000, 120000, 3_600_000, 5},
		{1, 3600000, 2 * 3600000, 20},
	}
	for ci, cfg := range cfgs {
		rng := newRng(ci)
		reg := hold.NewRegistry()
		s, err := New(cfg[0], cfg[1], cfg[2], cfg[3], reg)
		if err != nil {
			t.Fatal(err)
		}
		m1 := newNaive(cfg[0], cfg[1], cfg[2], cfg[3], reg)
		m2 := newNaive(cfg[0], cfg[1], cfg[2], cfg[3], reg)
		chk := func(i int) {
			t.Helper()
			if s.now != m1.now || !stateEqual(s.st, m1) || !stateEqual(s.st, m2) {
				t.Fatalf("cfg%d #%d 分歧 now=%d/%d/%d l0=%v m2l0=%v",
					ci, i, s.now, m1.now, m2.now, m1.l0, m2.l0)
			}
			if s.st.units() > s.cap {
				t.Fatalf("#%d units %d > cap", i, s.st.units())
			}
		}
		var clock int64
		for i := 0; i < 180; i++ {
			switch rng.Intn(10) {
			case 0, 1, 2:
				ts, v := rng.Int63n(12*3600000), rng.Int63n(2001)-1000
				mf := ts / 60000 * 60000
				held := reg.IntersectsInterval(mf, mf+60000)
				e := sameKind(s.Write(ts, v), m1.write(ts, v), m2.write(ts, v))
				t.Logf("cfg%d #%d Write(ts=%d,v=%d)->err=%v; now=%d 按A0=%d/A1=%d/A2=%d 判定, 分钟保全=%v",
					ci, i, ts, v, e, s.now, cfg[0], cfg[1], cfg[2], held)
				if e != nil {
					t.Fatalf("write 分歧 %v", e)
				}
			case 3, 4:
				clock += rng.Int63n(3_000_000) + 1
				e1, e2 := s.Advance(clock), m1.advance(clock)
				e3 := m2.advance(m2.now + (clock-m2.now)/2)
				if e3 == nil {
					e3 = m2.advance(clock)
				}
				t.Logf("cfg%d #%d Advance(%d)->%v/%v/%v; 三阶段删除/L0L1/L1L2 + 半开保全",
					ci, i, clock, e1, e2, e3)
				if sameKind(e1, e2, e3) != nil {
					t.Fatalf("advance 分歧 %v %v %v", e1, e2, e3)
				}
			case 5:
				f := rng.Int63n(3 * 3600000)
				to := f + rng.Int63n(600000) + 1
				id := holdID(i)
				t.Logf("cfg%d #%d Hold(%s,[%d,%d))->%v", ci, i, id, f, to, reg.Place(id, f, to, "o"))
			case 6:
				if sn := reg.Snapshot(); len(sn) > 0 {
					h := sn[rng.Intn(len(sn))]
					who := []string{h.Owner, "admin", "stranger"}[rng.Intn(3)]
					t.Logf("cfg%d #%d Release(%s,%s)->%v; 仅 owner/admin 可解除",
						ci, i, h.ID, who, reg.Release(h.ID, who))
				}
			default:
				f := rng.Int63n(3 * 3600000)
				to := f + rng.Int63n(3*3600000) + 1
				q1, err1 := s.Query(f, to)
				q2, q3 := m1.query(f, to), m2.query(f, to)
				t.Logf("cfg%d #%d Query([%d,%d))=%+v; 部分桶整桶跳过入 Skipped", ci, i, f, to, q1)
				if err1 != nil || q1 != q2 || q2 != q3 {
					t.Fatalf("query 分歧 %+v %+v %+v err=%v", q1, q2, q3, err1)
				}
			}
			chk(i)
		}
	}
}

func TestTierPrimitivesViaPackage(t *testing.T) {
	var b tier.Bucket
	if err := b.AddPoint(5); err != nil || b.AddPoint(-3) != nil {
		t.Fatal("add")
	}
	if b != (tier.Bucket{Count: 2, Sum: 2, Min: -3, Max: 5}) {
		t.Fatalf("b=%+v", b)
	}
	orig := tier.Bucket{Count: 1, Sum: math.MaxInt64, Min: 0, Max: 0}
	b = orig
	if err := b.AddPoint(1); !errors.Is(err, tier.ErrOverflow) || b != orig {
		t.Fatalf("add 溢出须不改桶: %+v err=%v", b, err)
	}
	if err := orig.Merge(tier.Bucket{Count: 1, Sum: 1, Min: 0, Max: 1}); !errors.Is(err, tier.ErrOverflow) {
		t.Fatal("merge 溢出")
	}
	if tier.MinuteOf(59999) != 0 || tier.MinuteOf(60000) != 1 ||
		tier.HourOf(3599999) != 0 || tier.HourOf(3600000) != 1 {
		t.Fatal("桶归属边界")
	}
	if tier.IntervalsOverlap(0, 10, 10, 20) || !tier.IntervalsOverlap(0, 10, 5, 15) {
		t.Fatal("半开相交")
	}
}

// 拒绝顺序：溢出先于容量；Advance 溢出整体回滚。
func TestOrderingAndOverflowRollback(t *testing.T) {
	s, _ := New(60000, 10*3600000, 100*3600000, 1, nil)
	s.now = 200000
	s.st.l1[0] = tier.Bucket{Count: 1, Sum: math.MaxInt64}
	if err := s.Write(10000, 1); !errors.Is(err, tier.ErrOverflow) {
		t.Fatalf("溢出应先于容量: %v", err)
	}
	if s.st.units() != 1 {
		t.Fatal("被拒写入改了状态")
	}
	s2, _ := New(1, 1, 100*3600000, 10, nil)
	s2.st.l1[0] = tier.Bucket{Count: 1, Sum: math.MaxInt64}
	s2.st.l1[1] = tier.Bucket{Count: 1, Sum: 1, Min: 1, Max: 1}
	if err := s2.Advance(10_000_000); !errors.Is(err, tier.ErrOverflow) {
		t.Fatalf("合并溢出: %v", err)
	}
	if s2.now != 0 || len(s2.st.l1) != 2 || len(s2.st.l2) != 0 {
		t.Fatalf("溢出后未回滚: now=%d l1=%v", s2.now, s2.st.l1)
	}
}

// TestConcurrentOps：-race 下无数据竞争，且最终单位数不超过 Cap。
func TestConcurrentOps(t *testing.T) {
	s, err := New(120000, 7_200_000, 86_400_000, 1000, nil)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 6; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				ts := int64((g*200 + i) * 1000)
				if ts > 5_000_000 {
					ts = ts % 5_000_000
				}
				_ = s.Write(ts, int64(g-i%7))
				_, _ = s.Query(0, 3_600_000)
				_ = s.Advance(int64(g) * 1000) // 多 goroutine 只有最大 now 生效
			}
		}(g)
	}
	wg.Wait()
	if s.Units() > 1000 {
		t.Fatalf("并发后单位数 %d 超过 Cap", s.Units())
	}
	t.Logf("并发结束 now=%d units=%d", s.Now(), s.Units())
}
