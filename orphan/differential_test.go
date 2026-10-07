package orphan_test

import (
	"fmt"
	"math/rand"
	"testing"

	"ontology/orphan"
	"ontology/orphan/naive"
)

// 随机操作序列下，索引化引擎的判定结果必须与独立朴素重放模型逐条一致。
func TestDifferentialAgainstNaive(t *testing.T) {
	for seed := int64(0); seed < 300; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runScenario(t, seed)
		})
	}
}

type scenario struct {
	t         *testing.T
	rng       *rand.Rand
	store     *orphan.Store
	clock     orphan.Time
	idc       int
	objTypes  []orphan.ObjectTypeID
	linkTypes []orphan.LinkTypeID
	objects   []orphan.ObjectID
	links     []orphan.LinkID
	determine int
}

func runScenario(t *testing.T, seed int64) {
	rng := rand.New(rand.NewSource(seed))
	sc := &scenario{
		t:         t,
		rng:       rng,
		objTypes:  []orphan.ObjectTypeID{"X", "Y", "Z"},
		linkTypes: []orphan.LinkTypeID{"L0", "L1", "L2"},
	}
	sc.store = orphan.NewStore(sc.randomSpec(), 0)

	// 初始链接类型（偶尔遗漏一个以制造悬置引用）。
	for _, lt := range sc.linkTypes {
		if rng.Intn(20) == 0 {
			continue
		}
		sc.append(orphan.Event{Kind: orphan.EvLinkTypeCreated, LinkType: lt, Time: 0})
	}

	steps := 40 + rng.Intn(60)
	for i := 0; i < steps; i++ {
		switch sc.rng.Intn(10) {
		case 0, 1, 2:
			sc.genObjectCreated()
		case 3, 4:
			sc.genLinkCreated()
		case 5:
			sc.genLinkRevoked()
		case 6:
			sc.genPropertySet()
		case 7:
			sc.genMarkedOrphan()
		case 8:
			sc.genAdjust()
		case 9:
			sc.compareDeterminations()
		}
	}
	sc.compareDeterminations()

	// 审计日志必须覆盖每一次判定。
	if got := len(sc.store.AuditLog()); got != sc.determine {
		t.Fatalf("seed=%d: audit entries %d, want %d", seed, got, sc.determine)
	}
}

func (sc *scenario) randomSpec() orphan.RuleSpec {
	spec := orphan.RuleSpec{GracePeriod: orphan.Time(sc.rng.Intn(5))}
	spec.RequiredLinkTypes = map[orphan.ObjectTypeID][]orphan.LinkTypeID{}
	for _, ot := range sc.objTypes {
		for _, lt := range sc.linkTypes {
			if sc.rng.Intn(2) == 0 {
				spec.RequiredLinkTypes[ot] = append(spec.RequiredLinkTypes[ot], lt)
			}
		}
	}
	return spec
}

func (sc *scenario) append(e orphan.Event) {
	sc.idc++
	e.ID = orphan.EventID(fmt.Sprintf("e%d", sc.idc))
	if err := sc.store.Append(e); err != nil {
		sc.t.Fatalf("append %+v: %v", e, err)
	}
}

func (sc *scenario) tick() orphan.Time {
	sc.clock += orphan.Time(sc.rng.Intn(3))
	return sc.clock
}

func (sc *scenario) genObjectCreated() {
	sc.idc++
	o := orphan.ObjectID(fmt.Sprintf("obj%d", sc.idc))
	sc.append(orphan.Event{Kind: orphan.EvObjectCreated, Object: o, ObjectType: sc.objTypes[sc.rng.Intn(len(sc.objTypes))], Time: sc.tick()})
	sc.objects = append(sc.objects, o)
}

func (sc *scenario) pickObject() (orphan.ObjectID, bool) {
	if len(sc.objects) == 0 || sc.rng.Intn(30) == 0 {
		return "ghost", false // 偶尔引用未创建对象
	}
	return sc.objects[sc.rng.Intn(len(sc.objects))], true
}

func (sc *scenario) genLinkCreated() {
	from, ok1 := sc.pickObject()
	to, ok2 := sc.pickObject()
	if !ok1 || !ok2 {
		return
	}
	sc.idc++
	l := orphan.LinkID(fmt.Sprintf("lnk%d", sc.idc))
	lt := sc.linkTypes[sc.rng.Intn(len(sc.linkTypes))]
	sc.append(orphan.Event{Kind: orphan.EvLinkCreated, Link: l, LinkType: lt, From: from, To: to, Time: sc.tick()})
	sc.links = append(sc.links, l)
}

func (sc *scenario) genLinkRevoked() {
	if len(sc.links) == 0 || sc.rng.Intn(30) == 0 {
		// 偶尔撤销不存在的链接（无法归属，判定不受影响）。
		sc.append(orphan.Event{Kind: orphan.EvLinkRevoked, Link: "ghostlink", Time: sc.tick()})
		return
	}
	l := sc.links[sc.rng.Intn(len(sc.links))]
	sc.append(orphan.Event{Kind: orphan.EvLinkRevoked, Link: l, Time: sc.tick()})
}

func (sc *scenario) genPropertySet() {
	o, ok := sc.pickObject()
	if !ok {
		return
	}
	at := sc.tick()
	// 偶尔制造同刻并列：一半概率补全因果链，一半概率留下歧义。
	if sc.rng.Intn(6) == 0 {
		sc.idc++
		first := orphan.EventID(fmt.Sprintf("e%d", sc.idc))
		sc.append(orphan.Event{ID: first, Kind: orphan.EvPropertySet, Object: o, Key: "k", Value: "a", Time: at})
		e := orphan.Event{Kind: orphan.EvPropertySet, Object: o, Key: "k", Value: "b", Time: at}
		if sc.rng.Intn(2) == 0 {
			e.After = first
		}
		sc.append(e)
		return
	}
	sc.append(orphan.Event{Kind: orphan.EvPropertySet, Object: o, Key: "k", Value: "v", Time: at})
}

func (sc *scenario) genMarkedOrphan() {
	o, ok := sc.pickObject()
	if !ok {
		return
	}
	sc.append(orphan.Event{Kind: orphan.EvObjectMarkedOrphan, Object: o, Time: sc.tick()})
}

func (sc *scenario) genAdjust() {
	ledger := sc.store.RuleLedger()
	at := ledger[len(ledger)-1].EffectiveFrom + 1 + orphan.Time(sc.rng.Intn(3))
	if at > sc.clock {
		sc.clock = at
	}
	if _, err := sc.store.AdjustRule(sc.randomSpec(), sc.rng.Intn(2) == 0, at); err != nil {
		sc.t.Fatalf("adjust: %v", err)
	}
}

func (sc *scenario) compareDeterminations() {
	if len(sc.objects) == 0 {
		return
	}
	ledger := sc.store.RuleLedger()
	events := sc.store.Events()
	for n := 0; n < 3; n++ {
		o := sc.objects[sc.rng.Intn(len(sc.objects))]
		at := orphan.Time(sc.rng.Intn(int(sc.clock) + 4))
		gov, ok := orphan.GoverningVersion(ledger, at)
		declared := 1
		if ok && sc.rng.Intn(10) < 7 {
			declared = gov.Version
		} else {
			declared = 1 + sc.rng.Intn(len(ledger)+1) // 可能声明错误版本
		}
		q := orphan.Query{Object: o, At: at, Version: declared}
		sc.determine++

		gotDet, gotErr := sc.store.Determine(q)
		if !ok || gov.Version != declared {
			if gotErr == nil || gotErr.(*orphan.Error).Code != orphan.ErrRuleVersionVoided {
				sc.t.Fatalf("q=%+v expected RuleVersionVoided, got %v", q, gotErr)
			}
			continue
		}
		wantDet, wantErr := naive.Determine(events, q, gov.Spec)
		if (gotErr == nil) != (wantErr == nil) {
			sc.t.Fatalf("q=%+v: engine err=%v naive err=%v", q, gotErr, wantErr)
		}
		if gotErr != nil {
			if gotErr.(*orphan.Error).Code != wantErr.Code {
				sc.t.Fatalf("q=%+v: engine err=%v naive err=%v", q, gotErr, wantErr)
			}
			continue
		}
		if !sameDetermination(gotDet, wantDet) {
			sc.t.Fatalf("q=%+v:\nengine=%+v\nnaive =%+v", q, gotDet, wantDet)
		}
	}
}

func sameDetermination(a, b orphan.Determination) bool {
	if a.Status != b.Status || a.ZeroSince != b.ZeroSince || a.OrphanDue != b.OrphanDue || a.ConfirmedAt != b.ConfirmedAt {
		return false
	}
	if len(a.Activities) != len(b.Activities) {
		return false
	}
	for i := range a.Activities {
		if a.Activities[i] != b.Activities[i] {
			return false
		}
	}
	return true
}
