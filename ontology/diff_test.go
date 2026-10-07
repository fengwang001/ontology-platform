package ontology_test

import (
	"math/rand"
	"reflect"
	"sort"
	"strconv"
	"testing"

	"ontology/ontology"
)

type dOpKind int

const (
	dStart dOpKind = iota
	dAmendNewAdd
	dAmendConflict
	dAmendFrozen
	dCreate
	dWrite
	dReadOld
	dReadNew
	dDelete
	dBackfill
)

type dOp struct {
	kind    dOpKind
	id      string
	ver     ontology.Version
	mapAttr string
	payload []string
}

var dIDs = []string{"k1", "k2", "k3", "k4", "k5"}
var dInitialAttrs = []string{"a", "b", "c"}
var dExtraAttrs = []string{"d", "e", "f", "g", "h"}

func valFor(attr string, salt int) ontology.Value {
	return ontology.Present(attr + "-v" + strconv.Itoa(salt))
}

func storeProps(attrs []string, salt int) ontology.Props {
	p := ontology.Props{}
	for _, attr := range attrs {
		p[ontology.AttrName(attr)] = valFor(attr, salt)
	}
	return p
}

func modelProps(attrs []string, salt int) map[string]ontology.Value {
	p := storeProps(attrs, salt)
	out := make(map[string]ontology.Value, len(p))
	for k, v := range p {
		out[string(k)] = v
	}
	return out
}

func knownAttrs(extraCount int) []string {
	out := append([]string{}, dInitialAttrs...)
	for i := 0; i < extraCount && i < len(dExtraAttrs); i++ {
		out = append(out, dExtraAttrs[i])
	}
	return out
}

func pickKnown(rng *rand.Rand, extraCount int) string {
	known := knownAttrs(extraCount)
	return known[rng.Intn(len(known))]
}

func fillPayload(rng *rand.Rand, op *dOp, extraCount int) {
	known := knownAttrs(extraCount)
	k := rng.Intn(len(known) + 1)
	perm := rng.Perm(len(known))
	for j := 0; j < k; j++ {
		op.payload = append(op.payload, known[perm[j]])
	}
}

func generateOps(rng *rand.Rand, n int) []dOp {
	ops := make([]dOp, 0, n)
	extraIdx := 0
	started := false
	for i := 0; i < n; i++ {
		if !started {
			if rng.Intn(3) != 0 {
				op := dOp{kind: dCreate, id: dIDs[rng.Intn(len(dIDs))], ver: ontology.VersionOld}
				fillPayload(rng, &op, 0)
				ops = append(ops, op)
				continue
			}
			ops = append(ops, dOp{kind: dStart})
			started = true
			continue
		}

		var op dOp
		switch rng.Intn(16) {
		case 0:
			if extraIdx < len(dExtraAttrs) {
				op = dOp{kind: dAmendNewAdd, mapAttr: dExtraAttrs[extraIdx]}
				extraIdx++
			} else {
				op = dOp{kind: dBackfill}
			}
		case 1:
			op = dOp{kind: dAmendConflict, mapAttr: pickKnown(rng, extraIdx)}
		case 2:
			op = dOp{kind: dAmendFrozen, mapAttr: pickKnown(rng, extraIdx)}
		case 3, 4:
			op = dOp{kind: dCreate, id: dIDs[rng.Intn(len(dIDs))], ver: ontology.Version(rng.Intn(2) + 1)}
			fillPayload(rng, &op, extraIdx)
		case 5, 6, 7:
			op = dOp{kind: dWrite, id: dIDs[rng.Intn(len(dIDs))], ver: ontology.Version(rng.Intn(2) + 1)}
			fillPayload(rng, &op, extraIdx)
		case 8, 9:
			op = dOp{kind: dReadOld, id: dIDs[rng.Intn(len(dIDs))]}
		case 10, 11:
			op = dOp{kind: dReadNew, id: dIDs[rng.Intn(len(dIDs))]}
		case 12:
			op = dOp{kind: dDelete, id: dIDs[rng.Intn(len(dIDs))]}
		default:
			op = dOp{kind: dBackfill}
		}
		ops = append(ops, op)
	}
	return ops
}

func initialMappings() ([]ontology.Mapping, []naiveMapping) {
	s := []ontology.Mapping{
		{Attr: "a", Kind: ontology.KindKeep},
		{Attr: "b", Kind: ontology.KindDrop},
		{Attr: "c", Kind: ontology.KindAdd, Default: ontology.Present("c-def")},
	}
	n := []naiveMapping{
		{attr: "a", kind: ontology.KindKeep},
		{attr: "b", kind: ontology.KindDrop},
		{attr: "c", kind: ontology.KindAdd, def: ontology.Present("c-def")},
	}
	return s, n
}

type traceLine struct {
	step  int
	input string
	got   string
	basis string
}

func runTrace(t *testing.T, ops []dOp, log bool) []traceLine {
	t.Helper()
	s := ontology.NewStore("T", ontology.VersionOld, ontology.VersionNew)
	m := newNaiveModel()
	traces := make([]traceLine, 0, len(ops))
	l := newLogger(t)

	for i, op := range ops {
		line := traceLine{step: i}
		switch op.kind {
		case dStart:
			sm, nm := initialMappings()
			se := s.StartMigration(ontology.Migration{ObjectType: "T", From: 1, To: 2, Mappings: sm})
			me := m.start(nm)
			line.input = "StartMigration(a:keep,b:drop,c:add)"
			line.got = classStr(classify(se))
			line.basis = "朴素模型=" + classStr(me)
			assertClass(t, se, me, line.input)
		case dAmendNewAdd:
			def := op.mapAttr + "-def"
			se := s.AmendDeclaration([]ontology.Mapping{
				{Attr: ontology.AttrName(op.mapAttr), Kind: ontology.KindAdd, Default: ontology.Present(def)},
			})
			me := m.amend([]naiveMapping{{attr: op.mapAttr, kind: ontology.KindAdd, def: ontology.Present(def)}})
			line.input = "Amend(add " + op.mapAttr + ")"
			line.got, line.basis = classStr(classify(se)), "朴素模型="+classStr(me)
			assertClass(t, se, me, line.input)
		case dAmendConflict:
			attr := ontology.AttrName(op.mapAttr)
			se := s.AmendDeclaration([]ontology.Mapping{
				{Attr: attr, Kind: ontology.KindKeep},
				{Attr: attr, Kind: ontology.KindDrop},
			})
			me := m.amend([]naiveMapping{
				{attr: op.mapAttr, kind: ontology.KindKeep},
				{attr: op.mapAttr, kind: ontology.KindDrop},
			})
			line.input = "Amend(conflict " + op.mapAttr + " keep&drop)"
			line.got, line.basis = classStr(classify(se)), "朴素模型="+classStr(me)
			assertClass(t, se, me, line.input)
		case dAmendFrozen:
			se := s.AmendDeclaration([]ontology.Mapping{
				{Attr: ontology.AttrName(op.mapAttr), Kind: ontology.KindDrop},
			})
			me := m.amend([]naiveMapping{{attr: op.mapAttr, kind: ontology.KindDrop}})
			line.input = "Amend(modify " + op.mapAttr + " ->drop)"
			line.got, line.basis = classStr(classify(se)), "朴素模型="+classStr(me)
			assertClass(t, se, me, line.input)
		case dCreate:
			salt := i % 7
			se := s.Create(op.id, op.ver, storeProps(op.payload, salt))
			me := m.create(op.id, op.ver, modelProps(op.payload, salt))
			line.input = "Create(" + op.id + "," + op.ver.String() + "," + joinAttrs(op.payload) + ")"
			line.got, line.basis = classStr(classify(se)), "朴素模型="+classStr(me)
			assertClass(t, se, me, line.input)
		case dWrite:
			salt := i % 7
			se := s.Write(op.id, op.ver, storeProps(op.payload, salt))
			me := m.write(op.id, op.ver, modelProps(op.payload, salt))
			line.input = "Write(" + op.id + "," + op.ver.String() + "," + joinAttrs(op.payload) + ")"
			line.got, line.basis = classStr(classify(se)), "朴素模型="+classStr(me)
			assertClass(t, se, me, line.input)
		case dReadOld, dReadNew:
			v := ontology.VersionOld
			if op.kind == dReadNew {
				v = ontology.VersionNew
			}
			gv, ge := s.Read(op.id, v)
			mv, me := m.read(op.id, v)
			line.input = "Read(" + op.id + "," + v.String() + ")"
			line.got = classStr(classify(ge)) + " " + fmtProps(gv)
			line.basis = "朴素模型=" + classStr(me) + " " + fmtModelProps(mv)
			if classify(ge) != me {
				t.Fatalf("步骤%d %s 错误类别不一致 store=%v model=%v", i, line.input, ge, me)
			}
			if ge == nil && !reflect.DeepEqual(normalizeProps(gv), normalizeModel(mv)) {
				t.Fatalf("步骤%d %s 视图不一致\nstore=%v\nmodel=%v", i, line.input, gv, mv)
			}
		case dDelete:
			se := s.Delete(op.id)
			me := m.delete(op.id)
			line.input = "Delete(" + op.id + ")"
			line.got, line.basis = classStr(classify(se)), "朴素模型="+classStr(me)
			assertClass(t, se, me, line.input)
		case dBackfill:
			so := s.RunBackfill()
			mo := m.backfill()
			line.input = "Backfill()"
			line.got = backfillStr(so)
			line.basis = "朴素模型=" + boolBf(mo)
			if so.DidWork != mo.didWork || so.Skipped != mo.skipped || so.Backfilled != mo.backfills ||
				(so.DidWork && so.ID != mo.id) {
				t.Fatalf("步骤%d 回填结果不一致 store=%+v model=%+v", i, so, mo)
			}
		}
		traces = append(traces, line)
		if log {
			l.step(line.input, line.got, line.basis)
		}
	}

	for {
		so := s.RunBackfill()
		mo := m.backfill()
		if so.DidWork != mo.didWork || so.Skipped != mo.skipped || so.Backfilled != mo.backfills {
			t.Fatalf("排空回填不一致 store=%+v model=%+v", so, mo)
		}
		if !so.DidWork {
			break
		}
	}
	for _, id := range dIDs {
		for _, v := range []ontology.Version{ontology.VersionOld, ontology.VersionNew} {
			gv, ge := s.Read(id, v)
			mv, me := m.read(id, v)
			if classify(ge) != me || (ge == nil && !reflect.DeepEqual(normalizeProps(gv), normalizeModel(mv))) {
				t.Fatalf("最终对照 %s/%s 不一致 store=(%v,%v) model=(%v,%v)", id, v, gv, ge, mv, me)
			}
		}
		sb, exists := s.IsBackfilled(id)
		minst := m.insts[id]
		modelExists := minst != nil && minst.exists
		if exists != modelExists {
			t.Fatalf("最终对照 %s 存在性不一致", id)
		}
		if modelExists && sb != minst.backfilled {
			t.Fatalf("最终对照 %s 回填状态不一致", id)
		}
	}
	return traces
}

func assertClass(t *testing.T, err error, want errClass, input string) {
	t.Helper()
	if classify(err) != want {
		t.Fatalf("%s: 错误类别不一致 store=%v 朴素模型=%v", input, err, want)
	}
}

func classStr(c errClass) string {
	switch c {
	case classOK:
		return "OK"
	case classInvalid:
		return "INVALID"
	case classNotFound:
		return "NOT-FOUND"
	default:
		return "?"
	}
}

func backfillStr(o ontology.BackfillOutcome) string {
	if !o.DidWork {
		return "IDLE"
	}
	flag := "BACKFILLED"
	if o.Skipped {
		flag = "SKIP(" + o.Reason + ")"
	}
	return o.ID + ":" + flag
}

func boolBf(r naiveBackfillResult) string {
	if !r.didWork {
		return "IDLE"
	}
	if r.skipped {
		return r.id + ":SKIP"
	}
	return r.id + ":BACKFILLED"
}

func joinAttrs(attrs []string) string {
	if len(attrs) == 0 {
		return "-"
	}
	out := append([]string{}, attrs...)
	sort.Strings(out)
	joined := ""
	for _, attr := range out {
		joined += attr + ","
	}
	return joined[:len(joined)-1]
}

type normVal struct {
	set bool
	val any
}

func normalizeProps(p ontology.Props) map[string]normVal {
	out := make(map[string]normVal, len(p))
	for k, v := range p {
		out[string(k)] = normVal{set: v.Set, val: v.Val}
	}
	return out
}

func normalizeModel(p map[string]ontology.Value) map[string]normVal {
	out := make(map[string]normVal, len(p))
	for k, v := range p {
		out[k] = normVal{set: v.Set, val: v.Val}
	}
	return out
}

func fmtMap(m map[string]normVal) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := "{"
	for _, k := range keys {
		if m[k].set {
			out += k + "=" + strconv.Quote(anyToStr(m[k].val)) + ","
		}
	}
	if len(keys) > 0 {
		out = out[:len(out)-1]
	}
	return out + "}"
}

func anyToStr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func fmtProps(p ontology.Props) string { return fmtMap(normalizeProps(p)) }

func fmtModelProps(p map[string]ontology.Value) string { return fmtMap(normalizeModel(p)) }

// TestRandomDifferential 用多组固定种子生成大量随机操作序列，
// 逐条把 Store 与朴素重算模型的错误类别、读取结果、回填结果、最终状态对照。
func TestRandomDifferential(t *testing.T) {
	const seeds = 60
	const opsPerSeed = 700
	for seed := int64(1); seed <= seeds; seed++ {
		rng := rand.New(rand.NewSource(seed))
		ops := generateOps(rng, opsPerSeed)
		runTrace(t, ops, false)
	}
	t.Logf("差分测试完成: %d 个种子 x %d 个操作, Store 与朴素模型逐条一致", seeds, opsPerSeed)
}

// TestReplayDeterministic：重放同一组操作与回填触发序列，必须得到完全相同的
// 轨迹与最终状态（打印一段可人工核对的输入/输出/依据）。
func TestReplayDeterministic(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	ops := generateOps(rng, 220)
	first := runTrace(t, ops, true)
	second := runTrace(t, ops, false)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("重放轨迹不一致")
	}
	t.Logf("重放 %d 个操作的逐条轨迹完全相同", len(ops))
}
