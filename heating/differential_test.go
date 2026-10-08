package heating_test

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"ontology/heating"
	"ontology/heating/naive"
)

// diffEnv 并行维护优化实现与朴素模型，逐步比对。
type diffEnv struct {
	t       *testing.T
	rng     *rand.Rand
	net     *heating.Network
	mod     *naive.Model
	seed    int64
	op      int
	nodes   []string
	segs    []string
	valves  []string
	segSeq  int
	valSeq  int
	nodeSeq int
}

func (e *diffEnv) fatalf(format string, args ...interface{}) {
	e.t.Helper()
	e.t.Fatalf("seed=%d op#%d: %s", e.seed, e.op, fmt.Sprintf(format, args...))
}

// checkErr 比对两个实现返回的错误（哨兵错误直接可比）。
func (e *diffEnv) checkErr(what string, e1, e2 error) {
	e.t.Helper()
	if e1 != e2 {
		e.fatalf("%s 错误不一致: net=%v naive=%v", what, e1, e2)
	}
}

// checkState 全量比对两个实现的可观察状态。
func (e *diffEnv) checkState() {
	e.t.Helper()
	for _, id := range e.nodes {
		if a, b := e.net.HasHeat(id), e.mod.HasHeat(id); a != b {
			e.fatalf("节点 %s 供热状态不一致: net=%v naive=%v", id, a, b)
		}
	}
	for _, id := range e.valves {
		s1, err1 := e.net.ValveStateOf(id)
		s2, err2 := e.mod.ValveStateOf(id)
		e.checkErr("阀门 "+id, err1, err2)
		if err1 == nil && s1 != s2 {
			e.fatalf("阀门 %s 状态不一致: net=%v naive=%v", id, s1, s2)
		}
	}
	for _, id := range e.segs {
		l1, err1 := e.net.SegmentLeaking(id)
		l2, err2 := e.mod.SegmentLeaking(id)
		e.checkErr("管段 "+id, err1, err2)
		if err1 == nil && l1 != l2 {
			e.fatalf("管段 %s 泄漏标记不一致: net=%v naive=%v", id, l1, l2)
		}
	}
	if a, b := e.net.ActiveIsolations(), e.mod.ActiveIsolations(); !reflect.DeepEqual(a, b) {
		e.fatalf("活动隔离不一致: net=%v naive=%v", a, b)
	}
}

func (e *diffEnv) pick(ids []string) string {
	return ids[e.rng.Intn(len(ids))]
}

// genTopology 生成随机拓扑：若干热源、分支点、用户入口，
// 先连成链保证连通，再随机添加平行/环形管段与阀门。
func genTopology(e *diffEnv) {
	rng := e.rng
	nSources := 1 + rng.Intn(2)
	nUsers := 2 + rng.Intn(3)
	nBranch := 3 + rng.Intn(6)
	total := nSources + nUsers + nBranch
	for i := 0; i < total; i++ {
		id := fmt.Sprintf("n%d", i)
		kind := heating.NodeBranch
		switch {
		case i < nSources:
			kind = heating.NodeSource
		case i >= total-nUsers:
			kind = heating.NodeUser
		}
		e.checkErr("AddNode", e.net.AddNode(id, kind), e.mod.AddNode(id, kind))
		e.nodes = append(e.nodes, id)
	}
	addSeg := func(a, b string) {
		id := fmt.Sprintf("s%d", e.segSeq)
		e.segSeq++
		e.checkErr("AddSegment", e.net.AddSegment(id, a, b), e.mod.AddSegment(id, a, b))
		e.segs = append(e.segs, id)
		if rng.Intn(2) == 0 {
			vid := fmt.Sprintf("v%d", e.valSeq)
			e.valSeq++
			e.checkErr("InstallValve", e.net.InstallValve(id, heating.EndA, vid), e.mod.InstallValve(id, heating.EndA, vid))
			e.valves = append(e.valves, vid)
		}
		if rng.Intn(2) == 0 {
			vid := fmt.Sprintf("v%d", e.valSeq)
			e.valSeq++
			e.checkErr("InstallValve", e.net.InstallValve(id, heating.EndB, vid), e.mod.InstallValve(id, heating.EndB, vid))
			e.valves = append(e.valves, vid)
		}
	}
	// 保证连通的骨架。
	for i := 1; i < total; i++ {
		addSeg(e.nodes[rng.Intn(i)], e.nodes[i])
	}
	// 额外随机管段（允许平行边）。
	for i := 0; i < total; i++ {
		a := e.nodes[rng.Intn(total)]
		b := e.nodes[rng.Intn(total)]
		if a != b {
			addSeg(a, b)
		}
	}
}

// step 执行一个随机操作并比对结果，日志打印输入、输出与判定依据。
func (e *diffEnv) step() {
	e.op++
	rng := e.rng
	kind := rng.Intn(12)
	switch kind {
	case 0, 1, 2, 3: // 推演（高频）
		seg := e.pick(e.segs)
		p1, err1 := e.net.SimulateIsolation(seg)
		p2, err2 := e.mod.Simulate(seg)
		e.checkErr("Simulate "+seg, err1, err2)
		if err1 == nil {
			if !reflect.DeepEqual(p1.ValvesToClose, p2.ValvesToClose) ||
				!reflect.DeepEqual(p1.Domain, p2.Domain) ||
				!reflect.DeepEqual(p1.AffectedUsers, p2.AffectedUsers) {
				e.fatalf("推演结果不一致:\nnet=   %+v\nnaive= %+v", p1, p2)
			}
			e.t.Logf("seed=%d op#%d simulate %s -> 关阀=%v 域=%v 停供=%v (判定依据: 边界可关即关, 不可关则并域)",
				e.seed, e.op, seg, p1.ValvesToClose, p1.Domain, p1.AffectedUsers)
		} else {
			e.t.Logf("seed=%d op#%d simulate %s -> err=%v", e.seed, e.op, seg, err1)
		}
	case 4: // 执行隔离（先推演拿到期望阀门集合）
		seg := e.pick(e.segs)
		var expected []string
		if p, err := e.net.SimulateIsolation(seg); err == nil && rng.Intn(2) == 0 {
			expected = p.ValvesToClose
		}
		p1, err1 := e.net.ExecuteIsolation(seg, expected)
		p2, err2 := e.mod.Execute(seg, expected)
		e.checkErr("Execute "+seg, err1, err2)
		if err1 == nil {
			if !reflect.DeepEqual(p1.ValvesToClose, p2.ValvesToClose) ||
				!reflect.DeepEqual(p1.Domain, p2.Domain) ||
				!reflect.DeepEqual(p1.AffectedUsers, p2.AffectedUsers) {
				e.fatalf("执行结果不一致:\nnet=   %+v\nnaive= %+v", p1, p2)
			}
		}
		e.t.Logf("seed=%d op#%d execute %s expected=%v -> err=%v", e.seed, e.op, seg, expected, err1)
	case 5: // 修复完成
		seg := e.pick(e.segs)
		err1 := e.net.CompleteRepair(seg)
		err2 := e.mod.Repair(seg)
		e.checkErr("Repair "+seg, err1, err2)
		e.t.Logf("seed=%d op#%d repair %s -> err=%v", e.seed, e.op, seg, err1)
	case 6: // 开阀
		v := e.pick(e.valves)
		err1 := e.net.OpenValve(v)
		err2 := e.mod.OpenValve(v)
		e.checkErr("Open "+v, err1, err2)
		e.t.Logf("seed=%d op#%d open %s -> err=%v", e.seed, e.op, v, err1)
	case 7: // 关阀
		v := e.pick(e.valves)
		err1 := e.net.CloseValve(v)
		err2 := e.mod.CloseValve(v)
		e.checkErr("Close "+v, err1, err2)
		e.t.Logf("seed=%d op#%d close %s -> err=%v", e.seed, e.op, v, err1)
	case 8: // 上报卡死
		v := e.pick(e.valves)
		st := heating.ValveStuckOpen
		if rng.Intn(2) == 0 {
			st = heating.ValveStuckClosed
		}
		err1 := e.net.ReportStuck(v, st)
		err2 := e.mod.ReportStuck(v, st)
		e.checkErr("Stuck "+v, err1, err2)
		e.t.Logf("seed=%d op#%d stuck %s %d -> err=%v", e.seed, e.op, v, st, err1)
	case 9: // 维修确认
		v := e.pick(e.valves)
		err1 := e.net.ConfirmValveRepaired(v)
		err2 := e.mod.ConfirmValveRepaired(v)
		e.checkErr("Confirm "+v, err1, err2)
		e.t.Logf("seed=%d op#%d confirm %s -> err=%v", e.seed, e.op, v, err1)
	case 10: // 增删管段
		if rng.Intn(2) == 0 {
			id := fmt.Sprintf("s%d", e.segSeq)
			e.segSeq++
			a := e.pick(e.nodes)
			b := e.pick(e.nodes)
			err1 := e.net.AddSegment(id, a, b)
			err2 := e.mod.AddSegment(id, a, b)
			e.checkErr("AddSegment "+id, err1, err2)
			e.segs = append(e.segs, id)
			e.t.Logf("seed=%d op#%d addseg %s %s-%s -> err=%v", e.seed, e.op, id, a, b, err1)
		} else {
			seg := e.pick(e.segs)
			err1 := e.net.RemoveSegment(seg)
			err2 := e.mod.RemoveSegment(seg)
			e.checkErr("RemoveSegment "+seg, err1, err2)
			e.t.Logf("seed=%d op#%d rmseg %s -> err=%v", e.seed, e.op, seg, err1)
		}
	case 11: // 增删阀门 / 新增节点
		switch rng.Intn(3) {
		case 0:
			seg := e.pick(e.segs)
			end := heating.End(rng.Intn(2))
			id := fmt.Sprintf("v%d", e.valSeq)
			e.valSeq++
			err1 := e.net.InstallValve(seg, end, id)
			err2 := e.mod.InstallValve(seg, end, id)
			e.checkErr("InstallValve "+id, err1, err2)
			e.valves = append(e.valves, id)
			e.t.Logf("seed=%d op#%d addvalve %s on %s[%d] -> err=%v", e.seed, e.op, id, seg, end, err1)
		case 1:
			seg := e.pick(e.segs)
			end := heating.End(rng.Intn(2))
			err1 := e.net.RemoveValve(seg, end)
			err2 := e.mod.RemoveValve(seg, end)
			e.checkErr("RemoveValve", err1, err2)
			e.t.Logf("seed=%d op#%d rmvalve %s[%d] -> err=%v", e.seed, e.op, seg, end, err1)
		default:
			id := fmt.Sprintf("x%d", e.nodeSeq)
			e.nodeSeq++
			k := heating.NodeKind(rng.Intn(3))
			err1 := e.net.AddNode(id, k)
			err2 := e.mod.AddNode(id, k)
			e.checkErr("AddNode "+id, err1, err2)
			e.nodes = append(e.nodes, id)
			e.t.Logf("seed=%d op#%d addnode %s kind=%d -> err=%v", e.seed, e.op, id, k, err1)
		}
	}
	e.checkState()
}

// TestDifferential 大量随机拓扑与操作序列下，优化实现与朴素模型须逐步一致。
func TestDifferential(t *testing.T) {
	for seed := int64(0); seed < 40; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			e := &diffEnv{
				t:   t,
				rng: rand.New(rand.NewSource(seed)),
				net: heating.NewNetwork(),
				mod: naive.New(),
			}
			e.seed = seed
			genTopology(e)
			e.checkState()
			steps := 120 + e.rng.Intn(80)
			for i := 0; i < steps; i++ {
				e.step()
			}
			t.Logf("seed=%d 完成 %d 步, 节点=%d 管段=%d 阀门=%d, 状态全量一致",
				seed, steps, len(e.nodes), len(e.segs), len(e.valves))
		})
	}
}
