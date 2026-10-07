package ontology

import (
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strings"
	"testing"
)

// 差分测试：对同一个随机操作序列，同时驱动
//   - 带缓存、最近优先剪枝的 Platform；
//   - 独立维护全量声明、每次完整重遍历角色层级的 NaivePlatform。
// 每一步都逐条对照错误类别、全部链接集合与每个操作者的可见集合；
// 同时打印输入、双方实际输出与判定依据，失败时打印完整轨迹便于复盘。

type diffOp struct {
	kind   int // 0 grantActor 1 grantRole 2 actorRole 3 roleContains 4 create 5 delete
	actor  string
	role   string
	inst   string
	prop   string
	vis    Visibility
	link   string
	source string
	target string
}

const (
	opGrantActor = iota
	opGrantRole
	opActorRole
	opRoleContains
	opCreate
	opDelete
)

var diffActors = []string{"alice", "bob", "carol", "dave"}
var diffDocs = []string{"d1", "d2", "d3", "d4"}
var diffPeople = []string{"p1", "p2", "p3", "p4"}
var diffRoles = []string{"r1", "r2", "r3", "r4", "r5"}

type diffWorld struct {
	real  *Platform
	naive *NaivePlatform
	log   *strings.Builder
}

func newDiffWorld() *diffWorld {
	w := &diffWorld{real: NewPlatform(), naive: NewNaivePlatform(), log: &strings.Builder{}}
	setup := func(define func(string, []PropertySpec), link func(LinkTypeSpec) *DecisionError,
		inst func(string, string) *DecisionError) {
		define("Doc", []PropertySpec{{Name: "docRef", LinkSource: true, DefaultVis: Invisible}})
		define("Person", []PropertySpec{{Name: "personRef", LinkSource: true, DefaultVis: Invisible}})
		if err := link(LinkTypeSpec{
			Name:      "authored",
			Source:    EndpointSpec{"Doc", "docRef"},
			Target:    EndpointSpec{"Person", "personRef"},
			SourceMax: 2,
			TargetMax: 2,
		}); err != nil {
			panic(err)
		}
		for _, d := range diffDocs {
			if err := inst(d, "Doc"); err != nil {
				panic(err)
			}
		}
		for _, q := range diffPeople {
			if err := inst(q, "Person"); err != nil {
				panic(err)
			}
		}
	}
	setup(w.real.DefineObjectType, w.real.DefineLinkType, w.real.CreateInstance)
	setup(w.naive.DefineObjectType, w.naive.DefineLinkType, w.naive.CreateInstance)
	return w
}

func (w *diffWorld) apply(op diffOp) (*DecisionError, *DecisionError) {
	switch op.kind {
	case opGrantActor:
		w.real.GrantActor(op.actor, op.inst, op.prop, op.vis)
		w.naive.GrantActor(op.actor, op.inst, op.prop, op.vis)
	case opGrantRole:
		w.real.GrantRole(op.role, op.inst, op.prop, op.vis)
		w.naive.GrantRole(op.role, op.inst, op.prop, op.vis)
	case opActorRole:
		w.real.AddActorRole(op.actor, op.role)
		w.naive.AddActorRole(op.actor, op.role)
	case opRoleContains:
		w.real.AddRoleContains(op.actor, op.role) // actor 字段复用为 contains
		w.naive.AddRoleContains(op.actor, op.role)
	case opCreate:
		e1 := w.real.CreateLink(op.actor, "authored", op.source, op.target)
		e2 := w.naive.CreateLink(op.actor, "authored", op.source, op.target)
		return e1, e2
	case opDelete:
		e1 := w.real.DeleteLink(op.actor, "authored", op.source, op.target)
		e2 := w.naive.DeleteLink(op.actor, "authored", op.source, op.target)
		return e1, e2
	}
	return nil, nil
}

func linksKey(ls []Link) string {
	parts := make([]string, len(ls))
	for i, l := range ls {
		parts[i] = l.Key()
	}
	sort.Strings(parts)
	return strings.Join(parts, "|")
}

func (w *diffWorld) opText(op diffOp) string {
	switch op.kind {
	case opGrantActor:
		return fmt.Sprintf("GrantActor(actor=%s inst=%s prop=%s vis=%s)", op.actor, op.inst, op.prop, op.vis)
	case opGrantRole:
		return fmt.Sprintf("GrantRole(role=%s inst=%s prop=%s vis=%s)", op.role, op.inst, op.prop, op.vis)
	case opActorRole:
		return fmt.Sprintf("AddActorRole(actor=%s role=%s)", op.actor, op.role)
	case opRoleContains:
		return fmt.Sprintf("AddRoleContains(contains=%s inner=%s)", op.actor, op.role)
	case opCreate:
		return fmt.Sprintf("CreateLink(actor=%s %s->%s)", op.actor, op.source, op.target)
	default:
		return fmt.Sprintf("DeleteLink(actor=%s %s->%s)", op.actor, op.source, op.target)
	}
}

// basis 按需求文字给出本步判定依据：两端可见性与两端当前占用。
func (w *diffWorld) basis(op diffOp) string {
	if op.kind != opCreate && op.kind != opDelete {
		return "schema/permission declaration"
	}
	srcVis := w.naive.Visibility(op.actor, op.source, "docRef")
	tgtVis := w.naive.Visibility(op.actor, op.target, "personRef")
	link := Link{LinkType: "authored", SourceInstance: op.source, TargetInstance: op.target}
	_, exists := w.naive.links[link.Key()]
	return fmt.Sprintf("naive basis: srcVis=%s tgtVis=%s exists=%v srcCount=%d/2 tgtCount=%d/2",
		srcVis, tgtVis, exists,
		w.naive.srcCount[slotKey{op.source, "docRef"}],
		w.naive.tgtCount[slotKey{op.target, "personRef"}])
}

func randomOp(rng *rand.Rand) diffOp {
	switch rng.Intn(10) {
	case 0, 1: // actor 覆盖
		return diffOp{
			kind:  opGrantActor,
			actor: diffActors[rng.Intn(len(diffActors))],
			inst:  append(diffDocs, diffPeople...)[rng.Intn(len(diffDocs)+len(diffPeople))],
			prop:  []string{"docRef", "personRef"}[rng.Intn(2)],
			vis:   Visibility(rng.Intn(2)),
		}
	case 2: // 角色覆盖（随机选 Doc 上的 docRef 或 Person 上的 personRef）
		if rng.Intn(2) == 0 {
			return diffOp{kind: opGrantRole, role: diffRoles[rng.Intn(len(diffRoles))],
				inst: diffDocs[rng.Intn(len(diffDocs))], prop: "docRef", vis: Visibility(rng.Intn(2))}
		}
		return diffOp{kind: opGrantRole, role: diffRoles[rng.Intn(len(diffRoles))],
			inst: diffPeople[rng.Intn(len(diffPeople))], prop: "personRef", vis: Visibility(rng.Intn(2))}
	case 3:
		return diffOp{kind: opActorRole,
			actor: diffActors[rng.Intn(len(diffActors))], role: diffRoles[rng.Intn(len(diffRoles))]}
	case 4:
		return diffOp{kind: opRoleContains,
			actor: diffRoles[rng.Intn(len(diffRoles))], role: diffRoles[rng.Intn(len(diffRoles))]}
	case 5, 6, 7, 8: // 创建概率高一些
		return diffOp{kind: opCreate, actor: diffActors[rng.Intn(len(diffActors))],
			source: diffDocs[rng.Intn(len(diffDocs))], target: diffPeople[rng.Intn(len(diffPeople))]}
	default:
		return diffOp{kind: opDelete, actor: diffActors[rng.Intn(len(diffActors))],
			source: diffDocs[rng.Intn(len(diffDocs))], target: diffPeople[rng.Intn(len(diffPeople))]}
	}
}

func TestDifferentialAgainstNaiveModel(t *testing.T) {
	// 默认静默运行；-v 或 ONTOLOGY_DIFF_VERBOSE=1 时逐步打印输入/输出/依据。
	verbose := testing.Verbose()
	if os.Getenv("ONTOLOGY_DIFF_VERBOSE") == "1" {
		verbose = true
	}

	const seeds, steps = 60, 300
	var mismatches int
	for seed := int64(1); seed <= seeds; seed++ {
		rng := rand.New(rand.NewSource(seed))
		w := newDiffWorld()
		for step := 0; step < steps; step++ {
			op := randomOp(rng)
			eReal, eNaive := w.apply(op)

			got, want := class(eReal), class(eNaive)
			line := fmt.Sprintf("seed=%d step=%d | INPUT: %s | OUTPUT real=%s naive=%s | %s",
				seed, step, w.opText(op), got, want, w.basis(op))
			if verbose {
				t.Log(line)
			} else {
				fmt.Fprintln(w.log, line)
			}
			if got != want {
				mismatches++
				t.Errorf("DECISION MISMATCH\n%s\ntrailing log:\n%s", line, tailLog(w.log))
				break
			}

			// 对照物理链接集合。
			if rk, nk := linksKey(w.real.AllLinks()), linksKey(w.naive.AllLinks()); rk != nk {
				mismatches++
				t.Errorf("LINK SET MISMATCH\n%s\nreal=[%s]\nnaive=[%s]", line, rk, nk)
				break
			}
			// 对照每个操作者的可见集合（含「存在性整体隐藏」语义）。
			for _, a := range diffActors {
				if rk, nk := linksKey(w.real.VisibleLinks(a)), linksKey(w.naive.VisibleLinks(a)); rk != nk {
					mismatches++
					t.Errorf("VISIBLE SET MISMATCH actor=%s\n%s\nreal=[%s]\nnaive=[%s]", a, line, rk, nk)
					break
				}
			}
			if mismatches > 0 {
				break
			}
		}
		if mismatches > 0 {
			break
		}
	}
	if mismatches == 0 {
		t.Logf("[differential] %d seeds x %d random ops: every decision, link set and per-actor visibility set matched the naive model",
			seeds, steps)
	}
}

func tailLog(b *strings.Builder) string {
	s := b.String()
	lines := strings.Split(s, "\n")
	if len(lines) > 20 {
		lines = lines[len(lines)-20:]
	}
	return strings.Join(lines, "\n")
}

// 重放确定性：同一 seed 的操作序列两次重放，最终链接集合必须完全一致。
func TestReplayDeterminism(t *testing.T) {
	run := func() string {
		rng := rand.New(rand.NewSource(42))
		w := newDiffWorld()
		for i := 0; i < 300; i++ {
			w.apply(randomOp(rng))
		}
		return linksKey(w.real.AllLinks())
	}
	first, second := run(), run()
	if first != second {
		t.Fatalf("replay must produce identical link sets\n%s\n%s", first, second)
	}
	t.Logf("[replay] identical final link set across replays: %d links", strings.Count(first, "|")+1)
}
