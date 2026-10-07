package ontology

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// randWorld 在生产模型与朴素模型上执行同构的初始建图命令。
func setupCommands() []Command {
	cmds := []Command{
		RegisterObjectType{Name: "Person", Attrs: []AttrSpec{
			{Name: "name", Default: Visible},
			{Name: "employer", Default: Visible},
		}},
		RegisterObjectType{Name: "Company", Attrs: []AttrSpec{
			{Name: "employee", Default: Visible},
		}},
		RegisterLinkType{Spec: LinkTypeSpec{
			Name: "employs", SrcType: "Person", SrcAttr: "employer",
			TgtType: "Company", TgtAttr: "employee", SrcMax: 2, TgtMax: 2,
		}},
	}
	for i := 0; i < 6; i++ {
		cmds = append(cmds, CreateInstance{ID: fmt.Sprintf("p%d", i), TypeName: "Person"})
	}
	for i := 0; i < 2; i++ {
		cmds = append(cmds, CreateInstance{ID: fmt.Sprintf("c%d", i), TypeName: "Company"})
	}
	for _, r := range []string{"R0", "R1", "R2", "R3", "R4"} {
		cmds = append(cmds, AddRole{Role: r})
	}
	return cmds
}

func randWorld(t *testing.T, seed int64) (*Arbiter, *NaiveModel, *rand.Rand) {
	t.Helper()
	a := NewArbiter()
	n := NewNaiveModel()
	for _, c := range setupCommands() {
		if err := a.Apply(c); err != nil {
			t.Fatalf("prod setup: %v", err)
		}
		if r := n.Apply(c); !r.OK {
			t.Fatalf("naive setup: %s", r.Code)
		}
	}
	return a, n, rand.New(rand.NewSource(seed))
}

var diffOperators = []string{"alice", "bob", "carol", "dave"}
var diffRoles = []string{"R0", "R1", "R2", "R3", "R4"}

func randCmd(rng *rand.Rand, step int) Command {
	op := rng.Intn(6)
	person := fmt.Sprintf("p%d", rng.Intn(6))
	company := fmt.Sprintf("c%d", rng.Intn(2))
	operator := diffOperators[rng.Intn(len(diffOperators))]
	switch op {
	case 0:
		return AssignRole{Operator: operator, Role: diffRoles[rng.Intn(len(diffRoles))]}
	case 1:
		return IncludeRole{
			Child:  diffRoles[rng.Intn(len(diffRoles))],
			Parent: diffRoles[rng.Intn(len(diffRoles))],
		}
	case 2:
		// 覆盖声明：目标属性在两端都可能出现。
		inst := person
		attr := "employer"
		if rng.Intn(2) == 0 {
			inst = company
			attr = "employee"
		}
		kind := SubjectOperator
		subject := operator
		if rng.Intn(2) == 0 {
			kind = SubjectRole
			subject = diffRoles[rng.Intn(len(diffRoles))]
		}
		return DeclareOverride{
			InstanceID: inst, Attr: attr, Kind: kind, Subject: subject,
			Vis: Visibility(rng.Intn(2)),
		}
	case 3:
		return CreateLink{
			ID:       fmt.Sprintf("L%d", step),
			TypeName: "employs",
			SrcID:    person, TgtID: company, Operator: operator,
		}
	case 4:
		// 一半概率尝试删除一个随机（可能从未存在）的链接 ID。
		id := fmt.Sprintf("L%d", rng.Intn(step+2))
		return DeleteLink{ID: id, Operator: operator}
	default:
		return SetTypeDefault{
			TypeName: "Person", Attr: "employer",
			Vis: Visibility(rng.Intn(2)),
		}
	}
}

func prodCode(err *ArbError) ErrorCode {
	if err != nil {
		return err.Code
	}
	return ""
}

func equalIDs(x, y []string) bool {
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

// TestRandomDifferential 在大量随机序列下逐条对照生产仲裁器与朴素模型：
// 每条命令的错误类别、物理链接集合、每个操作者的可见链接集合必须完全一致。
func TestRandomDifferential(t *testing.T) {
	const seeds = 24
	const steps = 300

	for seed := int64(1); seed <= seeds; seed++ {
		a, n, rng := randWorld(t, seed)
		echo(t, "===== DIFFERENTIAL seed=%d steps=%d =====", seed, steps)

		for step := 0; step < steps; step++ {
			cmd := randCmd(rng, step)

			var (
				code  ErrorCode
				extra string
			)
			switch c := cmd.(type) {
			case CreateLink:
				r := a.CreateLink(c)
				code = prodCode(r.Err)
				extra = fmt.Sprintf("src=%s tgt=%s",
					basisString(r.Evidence.SrcBasis), basisString(r.Evidence.TgtBasis))
			case DeleteLink:
				r := a.DeleteLink(c)
				code = prodCode(r.Err)
				extra = fmt.Sprintf("hiddenButExists=%v", r.HiddenButExists)
			default:
				code = prodCode(a.Apply(cmd))
			}

			nr := n.Apply(cmd)
			prodPhys := SortedIDs(a.PhysicalLinks())
			naivePhys := n.PhysicalIDs()
			echo(t, "[%03d seed=%d] INPUT  %#v", step, seed, cmd)
			echo(t, "[%03d seed=%d] OUTPUT prod=%s naive=%s %s", step, seed, code, nr.Code, extra)

			if code != nr.Code {
				t.Fatalf("seed=%d step=%d code mismatch: prod=%s naive=%s cmd=%#v",
					seed, step, code, nr.Code, cmd)
			}
			if !reflect.DeepEqual(prodPhys, naivePhys) {
				t.Fatalf("seed=%d step=%d physical mismatch: prod=%v naive=%v cmd=%#v",
					seed, step, prodPhys, naivePhys, cmd)
			}
			for _, op := range diffOperators {
				pv := SortedIDs(a.VisibleLinks(op))
				nv := n.VisibleIDs(op)
				echo(t, "[%03d seed=%d] VIS   op=%s prod=%v naive=%v", step, seed, op, pv, nv)
				if !equalIDs(pv, nv) {
					t.Fatalf("seed=%d step=%d visibility mismatch for %s: prod=%v naive=%v cmd=%#v",
						seed, step, op, pv, nv, cmd)
				}
			}
		}
	}
}

// TestReplayDeterminism 重放同一成功命令序列，必须得到完全相同的物理链接与可见性。
func TestReplayDeterminism(t *testing.T) {
	a, _, rng := randWorld(t, 777)
	var applied []Command
	for step := 0; step < 200; step++ {
		cmd := randCmd(rng, step)
		var err *ArbError
		switch c := cmd.(type) {
		case CreateLink:
			err = a.CreateLink(c).Err
		case DeleteLink:
			err = a.DeleteLink(c).Err
		default:
			err = a.Apply(cmd)
		}
		// 只重放成功生效的命令，得到一个合法的“已发生历史”。
		if err == nil {
			applied = append(applied, cmd)
		}
	}

	history := append(setupCommands(), applied...)
	r := Replay(history)
	echo(t, "INPUT  重放 %d 条历史命令（含建图）", len(history))
	echo(t, "OUTPUT 原始物理链接=%v", SortedIDs(a.PhysicalLinks()))
	echo(t, "OUTPUT 重放物理链接=%v", SortedIDs(r.PhysicalLinks()))
	if !reflect.DeepEqual(a.PhysicalLinks(), r.PhysicalLinks()) {
		t.Fatalf("replayed physical links differ")
	}
	for _, op := range diffOperators {
		av := SortedIDs(a.VisibleLinks(op))
		rv := SortedIDs(r.VisibleLinks(op))
		echo(t, "REPLAY op=%s orig=%v replayed=%v", op, av, rv)
		if !reflect.DeepEqual(av, rv) {
			t.Fatalf("replayed visibility differs for %s", op)
		}
	}
}
