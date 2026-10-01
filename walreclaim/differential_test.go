package walreclaim

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
)

// 操作种类。
const (
	opCreate = iota
	opWrite
	opRoll
	opFlushStart
	opFlushDone
	opDrop
	opObsolete
	opPurge
	opKindCount
)

type op struct {
	kind int
	name string
}

// snapshot 返回管理器当前状态的调试视图（cur、已 Purge 高水位、全部 first 多重集合）。
func snapshotOf(m *Manager) (cur, purged int64, firsts []int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	firsts = make([]int64, 0, len(m.firsts))
	for f, n := range m.firsts {
		for i := 0; i < n; i++ {
			firsts = append(firsts, f)
		}
	}
	sort.Slice(firsts, func(i, j int) bool { return firsts[i] < firsts[j] })
	return m.cur, m.purged, firsts
}

// outcome 是一次调用的可比较结果。
type outcome struct {
	err  error
	list []int64 // Obsolete / Purge
	val  int64   // Roll
}

func equalOutcome(a, b outcome) bool {
	if (a.err == nil) != (b.err == nil) {
		return false
	}
	if a.err != nil && !errors.Is(a.err, b.err) {
		return false
	}
	return a.val == b.val && reflect.DeepEqual(a.list, b.list)
}

// ---- 朴素参照实现：每次遍历全部内存表重算 ----

type naiveCF struct {
	active *int64
	frozen []int64
}

type naive struct {
	cur    int64
	cfs    map[string]*naiveCF
	purged map[int64]bool
}

func newNaive() *naive {
	return &naive{cur: 1, cfs: make(map[string]*naiveCF), purged: make(map[int64]bool)}
}

func (n *naive) allFirsts() []int64 {
	var fs []int64
	for _, c := range n.cfs {
		if c.active != nil {
			fs = append(fs, *c.active)
		}
		fs = append(fs, c.frozen...)
	}
	sort.Slice(fs, func(i, j int) bool { return fs[i] < fs[j] })
	return fs
}

// obsolete 每次都遍历全部未落盘内存表重算可回收前缀。
func (n *naive) obsolete() []int64 {
	var minFirst int64
	for _, f := range n.allFirsts() {
		if minFirst == 0 || f < minFirst {
			minFirst = f
		}
	}
	var out []int64
	for w := int64(1); w < n.cur; w++ {
		if n.purged[w] {
			continue
		}
		if minFirst != 0 && !(w < minFirst) {
			continue
		}
		out = append(out, w)
	}
	return out
}

func (n *naive) apply(o op) outcome {
	switch o.kind {
	case opCreate:
		if o.name == "" {
			return outcome{err: ErrEmptyName}
		}
		if _, ok := n.cfs[o.name]; ok {
			return outcome{err: ErrExists}
		}
		n.cfs[o.name] = &naiveCF{}
		return outcome{}
	case opWrite:
		if o.name == "" {
			return outcome{err: ErrEmptyName}
		}
		c, ok := n.cfs[o.name]
		if !ok {
			return outcome{err: ErrNotFound}
		}
		if c.active == nil {
			f := n.cur
			c.active = &f
		}
		return outcome{}
	case opRoll:
		n.cur++
		return outcome{val: n.cur}
	case opFlushStart:
		if o.name == "" {
			return outcome{err: ErrEmptyName}
		}
		c, ok := n.cfs[o.name]
		if !ok {
			return outcome{err: ErrNotFound}
		}
		if c.active == nil {
			return outcome{err: ErrEmptyActive}
		}
		c.frozen = append(c.frozen, *c.active)
		c.active = nil
		return outcome{}
	case opFlushDone:
		if o.name == "" {
			return outcome{err: ErrEmptyName}
		}
		c, ok := n.cfs[o.name]
		if !ok {
			return outcome{err: ErrNotFound}
		}
		if len(c.frozen) == 0 {
			return outcome{err: ErrEmptyFlushed}
		}
		c.frozen = c.frozen[1:]
		return outcome{}
	case opDrop:
		if o.name == "" {
			return outcome{err: ErrEmptyName}
		}
		if _, ok := n.cfs[o.name]; !ok {
			return outcome{err: ErrNotFound}
		}
		delete(n.cfs, o.name)
		return outcome{}
	case opObsolete:
		return outcome{list: n.obsolete()}
	case opPurge:
		out := n.obsolete()
		for _, w := range out {
			n.purged[w] = true
		}
		return outcome{list: out}
	default:
		panic("unknown op")
	}
}

func applyManager(m *Manager, o op) outcome {
	switch o.kind {
	case opCreate:
		return outcome{err: m.CreateCF(o.name)}
	case opWrite:
		return outcome{err: m.Write(o.name)}
	case opRoll:
		return outcome{val: m.Roll()}
	case opFlushStart:
		return outcome{err: m.FlushStart(o.name)}
	case opFlushDone:
		return outcome{err: m.FlushDone(o.name)}
	case opDrop:
		return outcome{err: m.DropCF(o.name)}
	case opObsolete:
		return outcome{list: m.Obsolete()}
	case opPurge:
		return outcome{list: m.Purge()}
	default:
		panic("unknown op")
	}
}

func opName(k int) string {
	switch k {
	case opCreate:
		return "CreateCF"
	case opWrite:
		return "Write"
	case opRoll:
		return "Roll"
	case opFlushStart:
		return "FlushStart"
	case opFlushDone:
		return "FlushDone"
	case opDrop:
		return "DropCF"
	case opObsolete:
		return "Obsolete"
	case opPurge:
		return "Purge"
	default:
		return "?"
	}
}

// generateOps 产生可完全重放的随机操作序列。
func generateOps(rng *rand.Rand, steps int) []op {
	const pool = 4 // cf0..cf3
	ops := make([]op, steps)
	for i := range ops {
		k := rng.Intn(opKindCount)
		var name string
		if k != opRoll && k != opObsolete && k != opPurge {
			switch rng.Intn(10) {
			case 0:
				name = "" // 约 1/10 概率触发空名拒绝路径
			default:
				name = fmt.Sprintf("cf%d", rng.Intn(pool))
			}
		}
		ops[i] = op{kind: k, name: name}
	}
	return ops
}

// TestRandomDifferential3000：与每次遍历全部内存表重算的朴素实现对拍 3000 步。
// 每一步打印输入、输出与判定依据（cur、全部 first、已 Purge 高水位、可回收集合）。
func TestRandomDifferential3000(t *testing.T) {
	runDifferential(t, 20261001, 3000)
}

func TestRandomDifferentialExtraSeeds(t *testing.T) {
	for _, seed := range []int64{1, 2, 42} {
		runDifferential(t, seed, 800)
	}
}

func runDifferential(t *testing.T, seed int64, steps int) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	ops := generateOps(rng, steps)

	m := NewManager()
	ref := newNaive()

	for i, o := range ops {
		got := applyManager(m, o)
		want := ref.apply(o)

		cur, purged, firsts := snapshotOf(m)
		basis := fmt.Sprintf("cur=%d allFirsts=%v purgedHigh=%d obsolete=%v",
			cur, firsts, purged, m.Obsolete())
		in := opName(o.kind)
		if o.name != "" || (o.kind != opRoll && o.kind != opObsolete && o.kind != opPurge) {
			in += "(" + fmt.Sprintf("%q", o.name) + ")"
		}
		t.Logf("seed=%d step=%d | IN: %s | OUT: %s | REF: %s | BECAUSE: %s",
			seed, i, in, formatOutcome(got), formatOutcome(want), basis)

		if !equalOutcome(got, want) {
			t.Fatalf("seed=%d step=%d %s mismatch: got %s, want %s (%s)",
				seed, i, in, formatOutcome(got), formatOutcome(want), basis)
		}
	}

	// 序列末尾对拍一次 Obsolete/Purge 并确认 Purge 后不重复。
	before := m.Obsolete()
	purged := m.Purge()
	if !reflect.DeepEqual(before, purged) {
		t.Fatalf("Purge %v != last Obsolete %v", purged, before)
	}
	if got := m.Obsolete(); len(got) != 0 {
		t.Fatalf("Obsolete after final Purge = %v, want empty", got)
	}
}

func formatOutcome(o outcome) string {
	if o.err != nil {
		return "err=" + o.err.Error()
	}
	if o.list != nil {
		return fmt.Sprintf("ok list=%v", o.list)
	}
	if o.val != 0 {
		return fmt.Sprintf("ok val=%d", o.val)
	}
	return "ok"
}

// TestDeterministicReplay：相同调用序列在两个全新管理器上重放，
// 每次 Obsolete/Purge 结果必须完全相同。
func TestDeterministicReplay(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	ops := generateOps(rng, 2000)

	var traces [2][]outcome
	for idx := range traces {
		m := NewManager()
		for _, o := range ops {
			traces[idx] = append(traces[idx], applyManager(m, o))
		}
	}
	if len(traces[0]) != len(traces[1]) {
		t.Fatalf("trace length mismatch")
	}
	for i := range traces[0] {
		if !equalOutcome(traces[0][i], traces[1][i]) {
			t.Fatalf("step %d non-deterministic: %s vs %s (op=%v)",
				i, formatOutcome(traces[0][i]), formatOutcome(traces[1][i]), ops[i])
		}
	}

	// 且与朴素实现的同序列结果一致。
	ref := newNaive()
	for i, o := range ops {
		want := ref.apply(o)
		if !equalOutcome(traces[0][i], want) {
			t.Fatalf("replay step %d diverges from naive: %s vs %s",
				i, formatOutcome(traces[0][i]), formatOutcome(want))
		}
	}
}

// TestConcurrentSafety：并发调用下做竞态检测与全局安全不变量检查——
// 任何时刻已回收编号集合不得包含仍被未落盘表依赖的日志。
func TestConcurrentSafety(t *testing.T) {
	m := NewManager()

	// 每个 goroutine 使用互不相同的列族名，避免“胜负难料”的拒绝分歧；
	// cur / Obsolete / Purge 是所有 goroutine 共享的压力点。
	const workers = 8
	const rounds = 400

	var workersWg sync.WaitGroup
	for g := 0; g < workers; g++ {
		name := fmt.Sprintf("worker%d", g)
		if err := m.CreateCF(name); err != nil {
			t.Fatalf("CreateCF(%s): %v", name, err)
		}
		workersWg.Add(1)
		go func(seed int64) {
			defer workersWg.Done()
			rng := rand.New(rand.NewSource(seed))
			active := false
			frozen := 0
			for r := 0; r < rounds; r++ {
				switch rng.Intn(6) {
				case 0:
					m.Roll()
				case 1, 2:
					if err := m.Write(name); err == nil {
						active = true
					}
				case 3:
					if err := m.FlushStart(name); err == nil {
						active = false
						frozen++
					}
				case 4:
					if err := m.FlushDone(name); err == nil && frozen > 0 {
						frozen--
					}
				case 5:
					m.Obsolete()
				}
			}
			_ = active
		}(int64(g + 7))
	}

	// 专职 Purger：不断取快照验证不变量后再 Purge。
	stop := make(chan struct{})
	var purgerWg sync.WaitGroup
	purgerWg.Add(1)
	go func() {
		defer purgerWg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_, purgedHigh, firsts := snapshotOf(m)
			for _, f := range firsts {
				if purgedHigh >= f {
					t.Errorf("safety violated: purged high water %d >= live memtable first %d",
						purgedHigh, f)
					return
				}
			}
			m.Purge()
		}
	}()

	workersWg.Wait()
	close(stop)
	purgerWg.Wait()

	// 收尾后再验一次全局不变量。
	_, purgedHigh, firsts := snapshotOf(m)
	for _, f := range firsts {
		if purgedHigh >= f {
			t.Fatalf("final safety violated: purged %d >= first %d", purgedHigh, f)
		}
	}
}
