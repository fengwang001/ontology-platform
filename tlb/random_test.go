package tlb

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

// op 是一条可重放的操作记录。
type op struct {
	kind     string
	cpu, mm  int
	vpn, pfn uint64
}

func (o op) String() string {
	switch o.kind {
	case "CreateMM":
		return "CreateMM()"
	case "Switch":
		return fmt.Sprintf("Switch(%d,%d)", o.cpu, o.mm)
	case "Fill":
		return fmt.Sprintf("Fill(%d,%d,%d)", o.cpu, o.vpn, o.pfn)
	case "Lookup":
		return fmt.Sprintf("Lookup(%d,%d)", o.cpu, o.vpn)
	case "Invalidate":
		return fmt.Sprintf("Invalidate(%d,%d)", o.mm, o.vpn)
	case "DestroyMM":
		return fmt.Sprintf("DestroyMM(%d)", o.mm)
	}
	return "?"
}

// runOp 在优化实现上执行一条操作，返回可比较的结果。
func runOp(m *Model, o op) string {
	switch o.kind {
	case "CreateMM":
		id, err := m.CreateMM()
		return fmt.Sprintf("id=%d err=%v", id, err)
	case "Switch":
		a, g, f, r, err := m.Switch(o.cpu, o.mm)
		return fmt.Sprintf("asid=%d gen=%d flushed=%v rolled=%v err=%v", a, g, f, r, err)
	case "Fill":
		ev, ok, err := m.Fill(o.cpu, o.vpn, o.pfn)
		return fmt.Sprintf("evicted=%v ok=%v err=%v", ev, ok, err)
	case "Lookup":
		pfn, hit, err := m.Lookup(o.cpu, o.vpn)
		return fmt.Sprintf("pfn=%d hit=%v err=%v", pfn, hit, err)
	case "Invalidate":
		n, err := m.Invalidate(o.mm, o.vpn)
		return fmt.Sprintf("removed=%d err=%v", n, err)
	case "DestroyMM":
		rel, n, err := m.DestroyMM(o.mm)
		return fmt.Sprintf("released=%v removed=%d err=%v", rel, n, err)
	}
	panic("bad op")
}

// runNaive 在朴素模拟上执行一条操作，返回可比较的结果。
func runNaive(n *naive, o op) string {
	switch o.kind {
	case "CreateMM":
		id, err := n.createMM()
		return fmt.Sprintf("id=%d err=%v", id, err)
	case "Switch":
		a, g, f, r, err := n.switchTo(o.cpu, o.mm)
		return fmt.Sprintf("asid=%d gen=%d flushed=%v rolled=%v err=%v", a, g, f, r, err)
	case "Fill":
		ev, ok, err := n.fill(o.cpu, o.vpn, o.pfn)
		return fmt.Sprintf("evicted=%v ok=%v err=%v", ev, ok, err)
	case "Lookup":
		pfn, hit, err := n.lookup(o.cpu, o.vpn)
		return fmt.Sprintf("pfn=%d hit=%v err=%v", pfn, hit, err)
	case "Invalidate":
		r, err := n.invalidate(o.mm, o.vpn)
		return fmt.Sprintf("removed=%d err=%v", r, err)
	case "DestroyMM":
		rel, r, err := n.destroyMM(o.mm)
		return fmt.Sprintf("released=%v removed=%d err=%v", rel, r, err)
	}
	panic("bad op")
}

// genOps 生成一条随机操作序列。
func genOps(r *rand.Rand, cfg Config, count int) []op {
	kinds := []string{"CreateMM", "Switch", "Switch", "Fill", "Fill", "Lookup", "Lookup", "Invalidate", "DestroyMM"}
	created := 0
	ops := make([]op, 0, count)
	for i := 0; i < count; i++ {
		k := kinds[r.Intn(len(kinds))]
		o := op{kind: k}
		// cpu：偶尔越界。
		o.cpu = r.Intn(cfg.CPUs+2) - 1
		// mm：偶尔未创建或越界。
		o.mm = r.Intn(created+3) - 1
		// vpn/pfn：小范围为主，偶尔越界。
		o.vpn = uint64(r.Intn(8))
		o.pfn = uint64(r.Intn(100))
		if r.Intn(20) == 0 {
			o.vpn = 1 << 32
		}
		if r.Intn(20) == 0 {
			o.pfn = 1 << 32
		}
		if k == "CreateMM" {
			created++
		}
		ops = append(ops, o)
	}
	return ops
}

// compareState 比较优化实现与朴素模拟的全部可观察状态。
func compareState(t *testing.T, m *Model, n *naive, seq, step int, o op) {
	t.Helper()
	where := fmt.Sprintf("seq=%d step=%d op=%s", seq, step, o)
	if m.Gen() != n.G {
		t.Fatalf("%s: G 不一致 model=%d naive=%d", where, m.Gen(), n.G)
	}
	if len(m.mms) != len(n.mms) {
		t.Fatalf("%s: mm 数不一致", where)
	}
	for i := range n.mms {
		a, g, alive := m.MM(i)
		nm := n.mms[i]
		if a != nm.asid || g != nm.gen || alive != nm.alive {
			t.Fatalf("%s: mm%d 不一致 model=(%d,%d,%v) naive=(%d,%d,%v)",
				where, i, a, g, alive, nm.asid, nm.gen, nm.alive)
		}
	}
	for cpu := 0; cpu < m.cfg.CPUs; cpu++ {
		act, res, pend := m.CPUState(cpu)
		nc := n.cpus[cpu]
		if act != (Pair{nc.active.asid, nc.active.gen}) ||
			res != (Pair{nc.reserved.asid, nc.reserved.gen}) ||
			pend != nc.pending {
			t.Fatalf("%s: cpu%d 状态不一致 model=(%v,%v,%v) naive=(%v,%v,%v)",
				where, cpu, act, res, pend, nc.active, nc.reserved, nc.pending)
		}
		got := m.TLBEntries(cpu)
		if len(got) != len(nc.tlb) {
			t.Fatalf("%s: TLB(%d) 长度不一致 model=%v naive=%v", where, cpu, got, nc.tlb)
		}
		for i, e := range got {
			ne := nc.tlb[i]
			if e.Key != (Key{ne.key.asid, ne.key.vpn}) || e.PFN != ne.pfn {
				t.Fatalf("%s: TLB(%d)[%d] 不一致 model=%v naive=%v", where, cpu, i, e, ne)
			}
		}
	}
	for a := 1; a <= m.cfg.ASIDs; a++ {
		if m.Taken(uint32(a)) != n.taken[uint32(a)] {
			t.Fatalf("%s: taken(%d) 不一致 model=%v naive=%v",
				where, a, m.Taken(uint32(a)), n.taken[uint32(a)])
		}
	}
}

// TestRandomAgainstNaive 用 2000 组随机操作序列对照优化实现与朴素模拟，
// 日志打印每步的输入、输出与判定依据；并对部分序列做重放一致性检查。
func TestRandomAgainstNaive(t *testing.T) {
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		r := rand.New(rand.NewSource(int64(seq)*7919 + 13))
		cfg := Config{
			ASIDs:  2 + r.Intn(6), // 2..7
			CPUs:   1 + r.Intn(3), // 1..3
			TLBCap: 1 + r.Intn(4), // 1..4
			MaxMMs: 4 + r.Intn(9), // 4..12
		}
		for cfg.ASIDs <= cfg.CPUs {
			cfg.ASIDs = cfg.CPUs + 1
		}
		ops := genOps(r, cfg, 60)
		m := mustNew(t, cfg)
		n := newNaive(cfg)
		t.Logf("seq=%d cfg=%+v ops=%d", seq, cfg, len(ops))
		var replay *Model
		if seq%100 == 0 {
			replay = mustNew(t, cfg)
		}
		for step, o := range ops {
			got := runOp(m, o)
			want := runNaive(n, o)
			// 判定依据：同一输入在两种实现上必须产生完全相同的输出，
			// 且事后全部可观察状态一致。
			t.Logf("seq=%d step=%d 输入=%s 输出=%s 判定=%v", seq, step, o, got, got == want)
			if got != want {
				t.Fatalf("seq=%d step=%d op=%s:\nmodel=%s\nnaive=%s", seq, step, o, got, want)
			}
			compareState(t, m, n, seq, step, o)
			if replay != nil {
				if again := runOp(replay, o); again != got {
					t.Fatalf("seq=%d step=%d op=%s 重放不一致: %s vs %s", seq, step, o, got, again)
				}
			}
		}
		if m.mmVisited != 0 {
			t.Fatalf("seq=%d: mmVisited=%d, want 0", seq, m.mmVisited)
		}
	}
}

// TestNaiveSanity 朴素模拟自身的错误值应与模型使用相同的哨兵错误。
func TestNaiveSanity(t *testing.T) {
	n := newNaive(Config{ASIDs: 2, CPUs: 1, TLBCap: 1, MaxMMs: 1})
	if _, _, _, _, err := n.switchTo(5, 0); !errors.Is(err, ErrBadCPU) {
		t.Fatalf("naive switchTo err = %v", err)
	}
	if _, err := n.createMM(); err != nil {
		t.Fatalf("naive createMM: %v", err)
	}
	if _, err := n.createMM(); !errors.Is(err, ErrTooManyMM) {
		t.Fatalf("naive createMM overflow err = %v", err)
	}
}
