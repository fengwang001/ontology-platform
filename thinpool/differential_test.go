package thinpool

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// op 是差分测试里与实现无关的操作记录。
type op struct {
	kind string
	name string
	a, b int
}

func (o op) String() string {
	return fmt.Sprintf("%s name=%q a=%d b=%d", o.kind, o.name, o.a, o.b)
}

// modelState 是从两种实现提取的可比状态。
type modelState struct {
	allocated    int
	free         int
	totalVirtual int
	totalReserve int
	totalDeficit int
	level        Level
	volumes      map[string][3]int // virtual, reservation, allocated
	mapped       map[string][]int  // 逐块映射（有序）
}

func extractNaive(m *naiveModel) modelState {
	st := modelState{
		allocated:    m.allocated,
		free:         m.free(),
		totalVirtual: m.totalVirtual,
		totalReserve: m.totalReserve,
		totalDeficit: m.totalDeficit(),
		level:        m.level,
		volumes:      map[string][3]int{},
		mapped:       map[string][]int{},
	}
	for _, name := range m.order {
		v := m.volumes[name]
		st.volumes[name] = [3]int{v.virtual, v.reservation, len(v.mapped)}
		blocks := make([]int, 0, len(v.mapped))
		for b := range v.mapped {
			blocks = append(blocks, b)
		}
		sort.Ints(blocks)
		if len(blocks) == 0 {
			blocks = nil
		}
		st.mapped[name] = blocks
	}
	return st
}

func extractPool(p *Pool) modelState {
	s := p.Snapshot()
	st := modelState{
		allocated:    s.Allocated,
		free:         s.Free,
		totalVirtual: s.TotalVirtual,
		totalReserve: s.TotalReserve,
		totalDeficit: s.TotalDeficit,
		level:        s.Level,
		volumes:      map[string][3]int{},
		mapped:       map[string][]int{},
	}
	p.mu.Lock()
	for _, v := range p.order {
		st.volumes[v.name] = [3]int{v.virtual, v.reservation, v.allocated()}
		var blocks []int
		for _, in := range v.mapped.snapshot() {
			for b := in.start; b < in.end; b++ {
				blocks = append(blocks, b)
			}
		}
		if len(blocks) == 0 {
			blocks = nil
		}
		st.mapped[v.name] = blocks
	}
	p.mu.Unlock()
	return st
}

func errKindOf(err error) Kind {
	if err == nil {
		return 0
	}
	if pe, ok := err.(*Error); ok {
		return pe.Kind
	}
	return -1
}

// runOnPool 对生产实现执行一条操作，返回 (bool结果, int结果, 错误类别, 判定依据)。
func runOnPool(p *Pool, o op) (bool, int, Kind, string) {
	switch o.kind {
	case "create":
		err := p.CreateVolume(o.name, o.a, o.b)
		return false, 0, errKindOf(err),
			fmt.Sprintf("overcommit (%d+%d)*100 <= %d*%d; reserve %d+%d <= %d",
				p.totalVirtual, o.a, p.cfg.PhysicalBlocks, p.cfg.OvercommitPct,
				p.totalReserve, o.b, p.cfg.PhysicalBlocks)
	case "write":
		got, err := p.Write(o.name, o.a)
		return got, 0, errKindOf(err),
			fmt.Sprintf("free-1=%d >= otherDeficit=%d (totalDeficit=%d O(1))",
				p.freeLocked()-1, func() int {
					if v := p.volumes[o.name]; v != nil {
						return p.totalDeficit - v.deficit()
					}
					return -1
				}(), p.totalDeficit)
	case "reclaim":
		got, err := p.Reclaim(o.name, o.a, o.b)
		return false, got, errKindOf(err), "runset.removeRange: 只遍历相交已映射区间"
	case "delete":
		err := p.DeleteVolume(o.name)
		return false, 0, errKindOf(err), "释放整卷映射并扣减三项记账"
	case "resize":
		err := p.Resize(o.name, o.a)
		return false, 0, errKindOf(err),
			fmt.Sprintf("overcommit虚拟和=%d 上限=floor(%d*%d/100)",
				p.totalVirtual, p.cfg.PhysicalBlocks, p.cfg.OvercommitPct)
	case "setres":
		err := p.SetReservation(o.name, o.a)
		return false, 0, errKindOf(err),
			fmt.Sprintf("reserveSum=%d <= %d 且 新欠额和=%d <= free=%d",
				p.totalReserve, p.cfg.PhysicalBlocks, p.totalDeficit, p.freeLocked())
	}
	return false, 0, 0, "unknown"
}

func runOnNaive(m *naiveModel, o op) (bool, int, Kind) {
	switch o.kind {
	case "create":
		return false, 0, errKindOf(m.create(o.name, o.a, o.b))
	case "write":
		got, err := m.write(o.name, o.a)
		return got, 0, errKindOf(err)
	case "reclaim":
		got, err := m.reclaim(o.name, o.a, o.b)
		return false, got, errKindOf(err)
	case "delete":
		return false, 0, errKindOf(m.del(o.name))
	case "resize":
		return false, 0, errKindOf(m.resize(o.name, o.a))
	case "setres":
		return false, 0, errKindOf(m.setReservation(o.name, o.a))
	}
	return false, 0, 0
}

func randomOp(rng *rand.Rand, names []string) op {
	namePool := append([]string{"a", "b", "c"}, names...)
	pick := func() string { return namePool[rng.Intn(len(namePool))] }
	kinds := []string{"create", "write", "write", "reclaim", "delete", "resize", "setres"}
	k := kinds[rng.Intn(len(kinds))]
	switch k {
	case "create":
		n := []string{"a", "b", "c", "d", "e"}[rng.Intn(5)]
		v := rng.Intn(8)
		r := rng.Intn(v + 1)
		return op{"create", n, v, r}
	case "write":
		return op{"write", pick(), rng.Intn(9), 0}
	case "reclaim":
		start := rng.Intn(9)
		return op{"reclaim", pick(), start, rng.Intn(9 - start)}
	case "delete":
		return op{"delete", pick(), 0, 0}
	case "resize":
		return op{"resize", pick(), rng.Intn(9), 0}
	case "setres":
		return op{"setres", pick(), rng.Intn(9), 0}
	}
	return op{}
}

func TestDifferentialRandomSequences(t *testing.T) {
	const iterations = 120
	const opCount = 250
	var logBuf []string
	for iter := 0; iter < iterations; iter++ {
		rng := rand.New(rand.NewSource(int64(1000 + iter)))
		cfg := Config{
			PhysicalBlocks: 4 + rng.Intn(10),
			OvercommitPct:  100 + rng.Intn(400),
			WarningPct:     30 + rng.Intn(30),
			CriticalPct:    70 + rng.Intn(30),
		}
		p, err := New(cfg)
		if err != nil {
			t.Fatalf("iter=%d New: %v", iter, err)
		}
		m := newNaive(cfg)
		var liveNames []string
		for step := 0; step < opCount; step++ {
			o := randomOp(rng, liveNames)
			pb, pi, pk, basis := runOnPool(p, o)
			mb, mi, mk := runOnNaive(m, o)
			outcome := func() string {
				if pk != 0 {
					return "REJECT " + pk.String()
				}
				switch o.kind {
				case "write":
					return fmt.Sprintf("OK allocatedNow=%v", pb)
				case "reclaim":
					return fmt.Sprintf("OK freed=%d", pi)
				default:
					return "OK"
				}
			}()
			logBuf = append(logBuf, fmt.Sprintf("iter=%d step=%d | %-40s | %-28s | basis: %s",
				iter, step, o.String(), outcome, basis))
			if pb != mb || pi != mi || pk != mk {
				t.Fatalf("iter=%d step=%d op=%s mismatch pool=(%v,%d,%s) naive=(%v,%d,%s)\nlast logs:\n%v",
					iter, step, o, pb, pi, pk, mb, mi, mk,
					tailLogs(logBuf, 30))
			}
			ps, ms := extractPool(p), extractNaive(m)
			if !reflect.DeepEqual(ps, ms) {
				t.Fatalf("iter=%d step=%d state mismatch after %s\npool=%+v\nnaive=%+v\nlast logs:\n%v",
					iter, step, o, ps, ms, tailLogs(logBuf, 30))
			}
			pEvents := p.Events()
			nEvents := m.events
			if len(pEvents) == 0 {
				pEvents = nil
			}
			if len(nEvents) == 0 {
				nEvents = nil
			}
			if !reflect.DeepEqual(pEvents, nEvents) {
				t.Fatalf("iter=%d step=%d events mismatch after %s\npool=%v\nnaive=%v",
					iter, step, o, p.Events(), m.events)
			}
			if o.kind == "create" && pk == 0 {
				liveNames = append(liveNames, o.name)
			}
			if o.kind == "delete" && pk == 0 {
				liveNames = removeName(liveNames, o.name)
			}
		}
	}
	t.Logf("differential log lines: %d (sample)\n%s", len(logBuf), tailLogs(logBuf, 20))
}

func tailLogs(lines []string, n int) []string {
	if len(lines) <= n {
		return lines
	}
	return lines[len(lines)-n:]
}

func removeName(names []string, name string) []string {
	out := names[:0]
	for _, n := range names {
		if n != name {
			out = append(out, n)
		}
	}
	return out
}
