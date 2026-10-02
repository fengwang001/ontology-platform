package compactor

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"
)

// naiveModel 是按规格逐步写成的朴素模型：保留全部历史记录，不做任何压实。
type naiveModel struct {
	deeper map[string]int64
	recs   []Record
}

func newNaiveModel(deeper map[string]int64) *naiveModel {
	d := make(map[string]int64, len(deeper))
	for k, v := range deeper {
		d[k] = v
	}
	return &naiveModel{deeper: d}
}

func (m *naiveModel) append(r Record) { m.recs = append(m.recs, r) }

func (m *naiveModel) get(key string, s uint64) (int64, bool) {
	var sum int64
	merged := false
	for i := len(m.recs) - 1; i >= 0; i-- {
		r := m.recs[i]
		if r.Key != key || r.Seq > s {
			continue
		}
		switch r.Kind {
		case KindPut:
			return sum + r.Value, true
		case KindDelete:
			return sum, merged
		case KindMerge:
			sum += r.Value
			merged = true
		}
	}
	base, ok := m.deeper[key]
	if !merged && !ok {
		return 0, false
	}
	if ok {
		sum += base
	}
	return sum, true
}

type view struct {
	key string
	s   uint64
}

type viewVal struct {
	v  int64
	ok bool
}

var keyPool = []string{"a", "b", "c", "d", "e", "f"}

// TestRandomAgainstNaiveModel 回放 2000 组随机操作序列，每组在每次 Compact 前后
// 于每个存活快照与 Latest 上比较所有键的 Get，并与朴素模型对照。
func TestRandomAgainstNaiveModel(t *testing.T) {
	const groups = 2000
	for g := 0; g < groups; g++ {
		runRandomGroup(t, g, g < 3)
	}
}

func runRandomGroup(t *testing.T, seed int, verbose bool) {
	t.Helper()
	rng := rand.New(rand.NewSource(int64(seed)*7919 + 13))

	deeper := make(map[string]int64)
	for _, k := range keyPool {
		if rng.Intn(4) == 0 {
			deeper[k] = int64(rng.Intn(41) - 20) // 含 0，存在即算有
		}
	}
	c := New(deeper)
	model := newNaiveModel(deeper)

	held := map[uint64]int{}
	usedKeys := map[string]bool{}
	for k := range deeper {
		usedKeys[k] = true
	}
	var trace []string
	logf := func(format string, args ...interface{}) {
		trace = append(trace, fmt.Sprintf(format, args...))
	}
	fail := func(format string, args ...interface{}) {
		t.Helper()
		t.Fatalf("group %d: %s\ndeeper=%v\n操作轨迹:\n%s",
			seed, fmt.Sprintf(format, args...), deeper, strings.Join(trace, "\n"))
	}

	liveSnapshots := func() []uint64 {
		ss := make([]uint64, 0, len(held))
		for s := range held {
			ss = append(ss, s)
		}
		sort.Slice(ss, func(i, j int) bool { return ss[i] < ss[j] })
		return ss
	}
	// 判定依据：压实前后任一存活快照与 Latest 的读取结果逐一相同，且与朴素模型一致。
	checkAllViews := func(phase string) map[view]viewVal {
		res := make(map[view]viewVal)
		snaps := append(liveSnapshots(), Latest)
		for k := range usedKeys {
			for _, s := range snaps {
				v, ok, err := c.Get(k, s)
				if err != nil {
					fail("%s: Get(%q,%d) err=%v", phase, k, s, err)
				}
				mv, mok := model.get(k, s)
				if v != mv || ok != mok {
					fail("%s: Get(%q,%d)=(%d,%v) 与朴素模型 (%d,%v) 不一致",
						phase, k, s, v, ok, mv, mok)
				}
				res[view{k, s}] = viewVal{v, ok}
			}
		}
		return res
	}

	logf("deeper=%v", deeper)
	compacts := 0
	n := 20 + rng.Intn(60)
	for i := 0; i < n; i++ {
		key := keyPool[rng.Intn(len(keyPool))]
		switch op := rng.Intn(100); {
		case op < 22:
			v := int64(rng.Intn(2001) - 1000)
			seq, err := c.Put(key, v)
			if err != nil {
				fail("Put err=%v", err)
			}
			model.append(Record{Key: key, Seq: seq, Kind: KindPut, Value: v})
			usedKeys[key] = true
			logf("Put(%s,%d)=%d", key, v, seq)
		case op < 44:
			d := int64(rng.Intn(2001) - 1000)
			seq, err := c.Merge(key, d)
			if err != nil {
				fail("Merge err=%v", err)
			}
			model.append(Record{Key: key, Seq: seq, Kind: KindMerge, Value: d})
			usedKeys[key] = true
			logf("Merge(%s,%d)=%d", key, d, seq)
		case op < 56:
			seq, err := c.Delete(key)
			if err != nil {
				fail("Delete err=%v", err)
			}
			model.append(Record{Key: key, Seq: seq, Kind: KindDelete})
			usedKeys[key] = true
			logf("Delete(%s)=%d", key, seq)
		case op < 68:
			s := c.Snapshot()
			held[s]++
			logf("Snapshot()=%d", s)
		case op < 78:
			ss := liveSnapshots()
			if len(ss) == 0 {
				if err := c.Release(uint64(rng.Intn(5))); err == nil {
					fail("Release 未持有序号却成功")
				}
				continue
			}
			s := ss[rng.Intn(len(ss))]
			if err := c.Release(s); err != nil {
				fail("Release(%d) err=%v", s, err)
			}
			held[s]--
			if held[s] == 0 {
				delete(held, s)
			}
			logf("Release(%d)", s)
		case op < 84:
			s := uint64(Latest)
			if ss := liveSnapshots(); len(ss) > 0 && rng.Intn(2) == 0 {
				s = ss[rng.Intn(len(ss))]
			}
			v, ok, err := c.Get(key, s)
			if err != nil {
				fail("Get(%q,%d) err=%v", key, s, err)
			}
			mv, mok := model.get(key, s)
			if v != mv || ok != mok {
				fail("Get(%q,%d)=(%d,%v) 与朴素模型 (%d,%v) 不一致", key, s, v, ok, mv, mok)
			}
		default:
			before := checkAllViews("压实前")
			inBefore := int64(len(c.Records()))
			st := c.Compact()
			compacts++
			// 账目守恒与计数。
			if st.In != inBefore {
				fail("Stats.In=%d 与压实前记录数 %d 不符", st.In, inBefore)
			}
			if st.In != st.Out+st.Shadowed+st.Folded+st.TombDropped {
				fail("Stats 不守恒: %v", st)
			}
			recs := c.Records()
			if st.Out != int64(len(recs)) {
				fail("Stats.Out=%d 与压实后记录数 %d 不符", st.Out, len(recs))
			}
			for j := 1; j < len(recs); j++ {
				if recs[j-1].Seq >= recs[j].Seq {
					fail("Records 未按序号严格升序: %v", recs)
				}
			}
			// 二分探测上限。
			snaps := len(held)
			bound := st.In * (floorLog2(int64(snaps)+1) + 1)
			if c.stripeProbes > bound {
				fail("stripeProbes=%d 超过上限 %d (In=%d, |S|=%d)", c.stripeProbes, bound, st.In, snaps)
			}
			// 压实后每个存活快照与 Latest 的读取结果与压实前逐一相同。
			after := checkAllViews("压实后")
			for vw, bv := range before {
				av := after[vw]
				if av != bv {
					fail("压实前后 Get(%q,%d) 不一致: 前 (%d,%v) 后 (%d,%v)",
						vw.key, vw.s, bv.v, bv.ok, av.v, av.ok)
				}
			}
			logf("Compact()=%v records=%v", st, recs)
		}
	}
	checkAllViews("收尾")
	if verbose {
		t.Logf("group %d 输入（deeper 与操作轨迹）:\n%s", seed, strings.Join(trace, "\n"))
		t.Logf("group %d 输出: 最终 records=%v, 共 %d 次 Compact", seed, c.Records(), compacts)
		t.Logf("group %d 判定依据: 每次 Compact 前后在每个存活快照与 Latest 上对所有键比较 Get，"+
			"并与保留全部历史的朴素模型对照；Stats 满足 In=Out+Shadowed+Folded+TombDropped", seed)
	}
}

// 相同操作序列重放得到完全相同的序号、产出与 Stats。
func TestReplayDeterminism(t *testing.T) {
	for seed := 0; seed < 20; seed++ {
		recs1, stats1 := simulate(t, seed)
		recs2, stats2 := simulate(t, seed)
		if fmt.Sprint(recs1) != fmt.Sprint(recs2) {
			t.Fatalf("seed %d: 重放 records 不一致:\n%v\n%v", seed, recs1, recs2)
		}
		if fmt.Sprint(stats1) != fmt.Sprint(stats2) {
			t.Fatalf("seed %d: 重放 Stats 不一致:\n%v\n%v", seed, stats1, stats2)
		}
	}
}

func simulate(t *testing.T, seed int) ([]Record, []Stats) {
	t.Helper()
	rng := rand.New(rand.NewSource(int64(seed)*104729 + 7))
	c := New(map[string]int64{"a": 3, "c": 0})
	var stats []Stats
	for i := 0; i < 120; i++ {
		key := keyPool[rng.Intn(len(keyPool))]
		switch rng.Intn(10) {
		case 0, 1, 2:
			if _, err := c.Put(key, int64(rng.Intn(200)-100)); err != nil {
				t.Fatal(err)
			}
		case 3, 4, 5:
			if _, err := c.Merge(key, int64(rng.Intn(200)-100)); err != nil {
				t.Fatal(err)
			}
		case 6:
			if _, err := c.Delete(key); err != nil {
				t.Fatal(err)
			}
		case 7:
			c.Snapshot()
		case 8:
			stats = append(stats, c.Compact())
		default:
			if _, _, err := c.Get(key, Latest); err != nil {
				t.Fatal(err)
			}
		}
	}
	stats = append(stats, c.Compact())
	return c.Records(), stats
}

// 全部方法并发调用：结果等价于某个串行顺序，Compact 是原子步骤。
// 配合 go test -race 检测数据竞争；结束后校验运行集序号严格升序且可读。
func TestConcurrent(t *testing.T) {
	c := New(map[string]int64{"a": 1})
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(id) + 1))
			key := keyPool[id%len(keyPool)]
			for i := 0; i < 500; i++ {
				switch rng.Intn(7) {
				case 0:
					c.Put(key, int64(rng.Intn(100)))
				case 1:
					c.Merge(key, int64(rng.Intn(100)))
				case 2:
					c.Delete(key)
				case 3:
					s := c.Snapshot()
					c.Get(key, s)
					c.Release(s)
				case 4:
					c.Get(key, Latest)
				case 5:
					c.Records()
				case 6:
					c.Compact()
				}
			}
		}(w)
	}
	wg.Wait()

	recs := c.Records()
	for i := 1; i < len(recs); i++ {
		if recs[i-1].Seq >= recs[i].Seq {
			t.Fatalf("并发后 Records 未按序号严格升序: %v", recs)
		}
	}
	st := c.Compact()
	if st.In != st.Out+st.Shadowed+st.Folded+st.TombDropped {
		t.Fatalf("并发后 Stats 不守恒: %v", st)
	}
	t.Logf("并发结束后 records=%d 条, Compact stats=%v", len(recs), st)
	t.Logf("判定依据: 所有方法由同一互斥锁串行化，任意并发交错等价于某个串行顺序，" +
		"Compact 在锁内一次性替换运行集，观察者看不到只重写部分键的中间状态")
}
