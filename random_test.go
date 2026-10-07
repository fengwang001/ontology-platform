package ontology_test

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"testing"

	"ontology"
	"ontology/naive"
)

// ---- 随机快照对生成器 ----

type gProp struct {
	id  string
	key string
	typ ontology.PropertyType
}

type gType struct {
	id      string
	props   []gProp // 含主键属性（props[0]）
	objects []int64
	nextPK  int64
}

type gLinkType struct {
	id  string
	src string
	dst string
}

type gLink struct {
	lt  string
	src int64
	dst int64
}

type generator struct {
	r         *rand.Rand
	b         *ontology.Builder
	types     []*gType
	linkTypes []*gLinkType
	links     []gLink
	ops       []string
	idSeq     int
}

func (g *generator) freshID(prefix string) string {
	g.idSeq++
	return fmt.Sprintf("%s-%d", prefix, g.idSeq)
}

var propTypes = []ontology.PropertyType{
	ontology.TypeString, ontology.TypeInt, ontology.TypeFloat, ontology.TypeBool,
}

func (g *generator) randType() ontology.PropertyType {
	return propTypes[g.r.Intn(len(propTypes))]
}

func randValue(r *rand.Rand, t ontology.PropertyType) ontology.Value {
	switch t {
	case ontology.TypeString:
		return ontology.StringValue(fmt.Sprintf("s%d", r.Intn(50)))
	case ontology.TypeInt:
		return ontology.IntValue(r.Int63n(100))
	case ontology.TypeFloat:
		return ontology.FloatValue(float64(r.Intn(100)) + 0.5)
	case ontology.TypeBool:
		return ontology.BoolValue(r.Intn(2) == 0)
	}
	panic("unreachable")
}

func (g *generator) findType(id string) *gType {
	for _, t := range g.types {
		if t.id == id {
			return t
		}
	}
	return nil
}

func (g *generator) findLinkType(id string) *gLinkType {
	for _, lt := range g.linkTypes {
		if lt.id == id {
			return lt
		}
	}
	return nil
}

// newGenerator 生成初始快照（rev=1）。
func newGenerator(seed int64) *generator {
	r := rand.New(rand.NewSource(seed))
	g := &generator{r: r, b: ontology.NewBuilder("L", 1)}
	nTypes := 1 + r.Intn(3)
	for i := 0; i < nTypes; i++ {
		gt := &gType{id: g.freshID("T"), nextPK: 1}
		gt.props = append(gt.props, gProp{id: g.freshID("pk"), key: "id", typ: ontology.TypeInt})
		for j := 0; j < 1+r.Intn(3); j++ {
			gt.props = append(gt.props, gProp{
				id: g.freshID("p"), key: g.freshID("k"), typ: g.randType(),
			})
		}
		props := make([]ontology.Property, 0, len(gt.props))
		for _, p := range gt.props {
			props = append(props, ontology.Property{ID: p.id, Key: p.key, Type: p.typ})
		}
		g.b.AddObjectType(gt.id, "Type"+gt.id, props[0], props[1:]...)
		nObj := r.Intn(9)
		for j := 0; j < nObj; j++ {
			g.addObjectTo(gt)
		}
		g.types = append(g.types, gt)
	}
	nLT := r.Intn(3)
	for i := 0; i < nLT; i++ {
		g.addLinkTypeOp()
	}
	nLinks := r.Intn(7)
	for i := 0; i < nLinks; i++ {
		g.addLinkOp()
	}
	return g
}

// randomValues 为类型的非主键属性生成随机取值（可能缺省）。
func (g *generator) randomValues(gt *gType) map[string]ontology.Value {
	vals := map[string]ontology.Value{}
	for _, p := range gt.props[1:] {
		if g.r.Float64() < 0.8 {
			vals[p.id] = randValue(g.r, p.typ)
		}
	}
	return vals
}

func (g *generator) addObjectTo(gt *gType) int64 {
	pk := gt.nextPK
	gt.nextPK++
	g.b.PutObject(gt.id, ontology.IntValue(pk), g.randomValues(gt))
	gt.objects = append(gt.objects, pk)
	return pk
}

// ---- 变更操作 ----

func (g *generator) addLinkTypeOp() {
	if len(g.types) == 0 {
		return
	}
	src := g.types[g.r.Intn(len(g.types))]
	dst := g.types[g.r.Intn(len(g.types))]
	lt := &gLinkType{id: g.freshID("LT"), src: src.id, dst: dst.id}
	g.b.AddLinkType(lt.id, "Link"+lt.id, lt.src, lt.dst)
	g.linkTypes = append(g.linkTypes, lt)
	g.ops = append(g.ops, "addLinkType "+lt.id)
}

func (g *generator) addLinkOp() {
	if len(g.linkTypes) == 0 {
		return
	}
	lt := g.linkTypes[g.r.Intn(len(g.linkTypes))]
	srcT, dstT := g.findType(lt.src), g.findType(lt.dst)
	if srcT == nil || dstT == nil || len(srcT.objects) == 0 || len(dstT.objects) == 0 {
		return
	}
	src := srcT.objects[g.r.Intn(len(srcT.objects))]
	dst := dstT.objects[g.r.Intn(len(dstT.objects))]
	for _, l := range g.links {
		if l.lt == lt.id && l.src == src && l.dst == dst {
			return
		}
	}
	g.b.PutLink(lt.id,
		ontology.ObjectRef{TypeID: lt.src, PK: ontology.IntValue(src)},
		ontology.ObjectRef{TypeID: lt.dst, PK: ontology.IntValue(dst)})
	g.links = append(g.links, gLink{lt: lt.id, src: src, dst: dst})
	g.ops = append(g.ops, fmt.Sprintf("addLink %s %d->%d", lt.id, src, dst))
}

func (g *generator) deleteLinkOp() {
	if len(g.links) == 0 {
		return
	}
	i := g.r.Intn(len(g.links))
	l := g.links[i]
	lt := g.findLinkType(l.lt)
	g.b.DeleteLink(l.lt,
		ontology.ObjectRef{TypeID: lt.src, PK: ontology.IntValue(l.src)},
		ontology.ObjectRef{TypeID: lt.dst, PK: ontology.IntValue(l.dst)})
	g.links = append(g.links[:i], g.links[i+1:]...)
	g.ops = append(g.ops, fmt.Sprintf("deleteLink %s %d->%d", l.lt, l.src, l.dst))
}

func (g *generator) addObjectOp() {
	if len(g.types) == 0 {
		return
	}
	gt := g.types[g.r.Intn(len(g.types))]
	pk := g.addObjectTo(gt)
	g.ops = append(g.ops, fmt.Sprintf("addObject %s/%d", gt.id, pk))
}

func (g *generator) updateObjectOp() {
	var candidates []*gType
	for _, t := range g.types {
		if len(t.objects) > 0 {
			candidates = append(candidates, t)
		}
	}
	if len(candidates) == 0 {
		return
	}
	gt := candidates[g.r.Intn(len(candidates))]
	pk := gt.objects[g.r.Intn(len(gt.objects))]
	g.b.PutObject(gt.id, ontology.IntValue(pk), g.randomValues(gt))
	g.ops = append(g.ops, fmt.Sprintf("updateObject %s/%d", gt.id, pk))
}

func (g *generator) deleteObjectOp() {
	var candidates []*gType
	for _, t := range g.types {
		if len(t.objects) > 0 {
			candidates = append(candidates, t)
		}
	}
	if len(candidates) == 0 {
		return
	}
	gt := candidates[g.r.Intn(len(candidates))]
	i := g.r.Intn(len(gt.objects))
	pk := gt.objects[i]
	g.b.DeleteObject(gt.id, ontology.IntValue(pk))
	gt.objects = append(gt.objects[:i], gt.objects[i+1:]...)
	// 级联删除触及该对象的链接（与 Builder 行为一致）。
	var kept []gLink
	for _, l := range g.links {
		lt := g.findLinkType(l.lt)
		if (lt.src == gt.id && l.src == pk) || (lt.dst == gt.id && l.dst == pk) {
			continue
		}
		kept = append(kept, l)
	}
	g.links = kept
	g.ops = append(g.ops, fmt.Sprintf("deleteObject %s/%d", gt.id, pk))
}

func (g *generator) addTypeOp() {
	gt := &gType{id: g.freshID("T"), nextPK: 1}
	gt.props = append(gt.props, gProp{id: g.freshID("pk"), key: "id", typ: ontology.TypeInt})
	for j := 0; j < 1+g.r.Intn(2); j++ {
		gt.props = append(gt.props, gProp{id: g.freshID("p"), key: g.freshID("k"), typ: g.randType()})
	}
	props := make([]ontology.Property, 0, len(gt.props))
	for _, p := range gt.props {
		props = append(props, ontology.Property{ID: p.id, Key: p.key, Type: p.typ})
	}
	g.b.AddObjectType(gt.id, "Type"+gt.id, props[0], props[1:]...)
	for j := 0; j < g.r.Intn(4); j++ {
		g.addObjectTo(gt)
	}
	g.types = append(g.types, gt)
	g.ops = append(g.ops, "addType "+gt.id)
}

func (g *generator) removeTypeOp() {
	if len(g.types) == 0 {
		return
	}
	i := g.r.Intn(len(g.types))
	gt := g.types[i]
	g.b.RemoveObjectType(gt.id)
	g.types = append(g.types[:i], g.types[i+1:]...)
	// 同步模型：级联移除引用该类型的链接类型及其实例。
	var keptLT []*gLinkType
	removedLT := map[string]bool{}
	for _, lt := range g.linkTypes {
		if lt.src == gt.id || lt.dst == gt.id {
			removedLT[lt.id] = true
			continue
		}
		keptLT = append(keptLT, lt)
	}
	g.linkTypes = keptLT
	var kept []gLink
	for _, l := range g.links {
		if removedLT[l.lt] {
			continue
		}
		kept = append(kept, l)
	}
	g.links = kept
	g.ops = append(g.ops, "removeType "+gt.id)
}

func (g *generator) addPropOp() {
	if len(g.types) == 0 {
		return
	}
	gt := g.types[g.r.Intn(len(g.types))]
	p := gProp{id: g.freshID("p"), key: g.freshID("k"), typ: g.randType()}
	g.b.AddProperty(gt.id, ontology.Property{ID: p.id, Key: p.key, Type: p.typ})
	gt.props = append(gt.props, p)
	g.ops = append(g.ops, fmt.Sprintf("addProp %s.%s", gt.id, p.id))
}

func (g *generator) removePropOp() {
	var targets []*gType
	for _, t := range g.types {
		if len(t.props) > 1 {
			targets = append(targets, t)
		}
	}
	if len(targets) == 0 {
		return
	}
	gt := targets[g.r.Intn(len(targets))]
	i := 1 + g.r.Intn(len(gt.props)-1)
	p := gt.props[i]
	g.b.RemoveProperty(gt.id, p.id)
	gt.props = append(gt.props[:i], gt.props[i+1:]...)
	g.ops = append(g.ops, fmt.Sprintf("removeProp %s.%s", gt.id, p.id))
}

func (g *generator) renamePropOp() {
	if len(g.types) == 0 {
		return
	}
	gt := g.types[g.r.Intn(len(g.types))]
	i := g.r.Intn(len(gt.props)) // 主键属性也可重命名
	newKey := g.freshID("k")
	g.b.RenameProperty(gt.id, gt.props[i].id, newKey)
	g.ops = append(g.ops, fmt.Sprintf("renameProp %s.%s -> %s", gt.id, gt.props[i].id, newKey))
	gt.props[i].key = newKey
}

func (g *generator) retypePropOp() {
	var targets []*gType
	for _, t := range g.types {
		if len(t.props) > 1 {
			targets = append(targets, t)
		}
	}
	if len(targets) == 0 {
		return
	}
	gt := targets[g.r.Intn(len(targets))]
	i := 1 + g.r.Intn(len(gt.props)-1)
	newT := g.randType()
	if newT == gt.props[i].typ {
		return
	}
	g.b.RetypeProperty(gt.id, gt.props[i].id, newT)
	g.ops = append(g.ops, fmt.Sprintf("retypeProp %s.%s -> %s", gt.id, gt.props[i].id, newT))
	gt.props[i].typ = newT
}

func (g *generator) renameAndRetypePropOp() {
	g.renamePropOp()
	g.retypePropOp()
}

// retypePKOp 触发错误类别三：主键改型。此后不再生成更多操作。
func (g *generator) retypePKOp() {
	if len(g.types) == 0 {
		return
	}
	gt := g.types[g.r.Intn(len(g.types))]
	g.b.RetypeProperty(gt.id, gt.props[0].id, ontology.TypeFloat)
	g.ops = append(g.ops, "retypePK "+gt.id)
}

// changePKOp 触发错误类别三：主键指定更换。此后不再生成更多操作。
func (g *generator) changePKOp() {
	if len(g.types) == 0 {
		return
	}
	gt := g.types[g.r.Intn(len(g.types))]
	p := gProp{id: g.freshID("pk2"), key: g.freshID("k"), typ: ontology.TypeString}
	g.b.AddProperty(gt.id, ontology.Property{ID: p.id, Key: p.key, Type: p.typ})
	for i, pk := range gt.objects {
		vals := g.randomValues(gt)
		vals[p.id] = ontology.StringValue(fmt.Sprintf("k%d-%d", g.idSeq, i))
		g.b.PutObject(gt.id, ontology.IntValue(pk), vals)
	}
	g.b.ChangePrimaryKey(gt.id, p.id)
	g.ops = append(g.ops, "changePK "+gt.id)
}

// setLinkEndpointsOp 触发错误类别三：链接类型端点更换。
func (g *generator) setLinkEndpointsOp() {
	if len(g.linkTypes) == 0 || len(g.types) == 0 {
		return
	}
	lt := g.linkTypes[g.r.Intn(len(g.linkTypes))]
	src := g.types[g.r.Intn(len(g.types))]
	dst := g.types[g.r.Intn(len(g.types))]
	g.b.SetLinkTypeEndpoints(lt.id, src.id, dst.id)
	g.ops = append(g.ops, fmt.Sprintf("setLinkEndpoints %s -> (%s,%s)", lt.id, src.id, dst.id))
}

func (g *generator) removeLinkTypeOp() {
	if len(g.linkTypes) == 0 {
		return
	}
	i := g.r.Intn(len(g.linkTypes))
	lt := g.linkTypes[i]
	g.b.RemoveLinkType(lt.id)
	g.linkTypes = append(g.linkTypes[:i], g.linkTypes[i+1:]...)
	var kept []gLink
	for _, l := range g.links {
		if l.lt != lt.id {
			kept = append(kept, l)
		}
	}
	g.links = kept
	g.ops = append(g.ops, "removeLinkType "+lt.id)
}

// mutate 从初始快照出发施加一批随机变更，返回目标快照。
// 返回值的布尔标记表示是否触发了类别三（口径无法确定）操作。
func (g *generator) mutate() (*ontology.Snapshot, bool) {
	s0 := g.b.Build()
	g.b = ontology.FromSnapshot(s0)
	g.b.Advance(2)
	n := 3 + g.r.Intn(15)
	basisBroken := false
	for i := 0; i < n; i++ {
		switch g.r.Intn(100) {
		case 0, 1:
			g.addTypeOp()
		case 2, 3:
			g.removeTypeOp()
		case 4, 5, 6:
			g.addPropOp()
		case 7, 8:
			g.removePropOp()
		case 9, 10, 11:
			g.renamePropOp()
		case 12, 13:
			g.retypePropOp()
		case 14, 15:
			g.renameAndRetypePropOp()
		case 16:
			g.retypePKOp()
			basisBroken = true
		case 17:
			g.changePKOp()
			basisBroken = true
		case 18:
			g.setLinkEndpointsOp()
			basisBroken = true
		case 19:
			g.removeLinkTypeOp()
		case 20, 21:
			g.addLinkTypeOp()
		case 22, 23, 24:
			g.addLinkOp()
		case 25, 26:
			g.deleteLinkOp()
		case 27, 28, 29:
			g.addObjectOp()
		case 30, 31, 32, 33:
			g.updateObjectOp()
		case 34, 35:
			g.deleteObjectOp()
		default:
			// 空操作批次：制造无差异或近似无差异的快照对。
		}
		if basisBroken {
			break
		}
	}
	return g.b.Build(), basisBroken
}

// ---- 比对记录 ----

// compareRecord 记录一次比对的输入、输出与判定依据，
// 用于复核与失败重放。
type compareRecord struct {
	Seed       int64    `json:"seed"`
	Ops        []string `json:"ops"`
	OldFP      string   `json:"oldFingerprint"`
	NewFP      string   `json:"newFingerprint"`
	ErrorClass string   `json:"errorClass,omitempty"`
	Result     string   `json:"result,omitempty"`
}

func (rec compareRecord) json(t *testing.T) string {
	t.Helper()
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal record: %v", err)
	}
	return string(data)
}

// diffPayload 是用于跨实现比对的输出部分（不含实现相关的 Stats）。
type diffPayload struct {
	Schema    *ontology.SchemaDiff   `json:"schema"`
	Instances *ontology.InstanceDiff `json:"instances"`
}

func payloadOf(res *ontology.Result) string {
	data, err := json.Marshal(diffPayload{Schema: res.Schema, Instances: res.Instances})
	if err != nil {
		panic(err)
	}
	return string(data)
}

func runPair(t *testing.T, seed int64, s0, s1 *ontology.Snapshot, ops []string) {
	t.Helper()
	resMain, errMain := ontology.Compare(s0, s1)
	resNaive, errNaive := naive.Compare(s0, s1)

	rec := compareRecord{
		Seed:  seed,
		Ops:   ops,
		OldFP: ontology.Fingerprint(s0),
		NewFP: ontology.Fingerprint(s1),
	}

	classMain, classNaive := ontology.ErrorClassOf(errMain), ontology.ErrorClassOf(errNaive)
	if classMain != classNaive {
		rec.ErrorClass = fmt.Sprintf("main=%q naive=%q", classMain, classNaive)
		t.Fatalf("error class mismatch, record: %s", rec.json(t))
	}
	rec.ErrorClass = string(classMain)
	if errMain != nil {
		recordComparison(t, rec)
		return
	}

	pMain, pNaive := payloadOf(resMain), payloadOf(resNaive)
	rec.Result = pMain
	if pMain != pNaive {
		rec.Result = fmt.Sprintf("main=%s naive=%s", pMain, pNaive)
		t.Fatalf("result mismatch, record: %s", rec.json(t))
	}

	// 判定依据必须可从输出中读出：每条差异都带类别与原因。
	assertBasisPresent(t, resMain)

	// 同一输入重复比对，结果不得漂移。
	again, err := ontology.Compare(s0, s1)
	if err != nil {
		t.Fatalf("repeat compare failed: %v", err)
	}
	if payloadOf(again) != pMain {
		t.Fatalf("non-deterministic result, record: %s", rec.json(t))
	}

	recordComparison(t, rec)
}

func assertBasisPresent(t *testing.T, res *ontology.Result) {
	t.Helper()
	for _, od := range res.Instances.Objects {
		switch od.Kind {
		case ontology.ObjectCreated, ontology.ObjectUpdated:
		case ontology.ObjectDeleted:
			if od.Reason != ontology.ReasonObjectExplicit && od.Reason != ontology.ReasonTypeDeprecated {
				t.Fatalf("object diff missing delete reason: %+v", od)
			}
		default:
			t.Fatalf("object diff missing kind: %+v", od)
		}
	}
	for _, ld := range res.Instances.Links {
		switch ld.Kind {
		case ontology.LinkCreated:
		case ontology.LinkDeleted:
			if ld.Reason == "" {
				t.Fatalf("link diff missing delete reason: %+v", ld)
			}
		default:
			t.Fatalf("link diff missing kind: %+v", ld)
		}
	}
}

// recordComparison 持久化比对记录：设置环境变量 ONTDIFF_RECORD
// 时以 JSONL 追加写入指定文件；否则仅保留在内存中供失败时输出。
func recordComparison(t *testing.T, rec compareRecord) {
	t.Helper()
	path := os.Getenv("ONTDIFF_RECORD")
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("open record file: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString(rec.json(t) + "\n"); err != nil {
		t.Fatalf("write record: %v", err)
	}
}

// TestRandomPairsAgainstNaive 在大量随机生成的快照对上，
// 将主实现与按规则逐步独立判定的朴素参照模型对照。
func TestRandomPairsAgainstNaive(t *testing.T) {
	iterations := 300
	if v := os.Getenv("ONTDIFF_ITERATIONS"); v != "" {
		if _, err := fmt.Sscanf(v, "%d", &iterations); err != nil {
			t.Fatalf("bad ONTDIFF_ITERATIONS: %v", err)
		}
	}
	var statsErr, statsOK int
	for i := 0; i < iterations; i++ {
		seed := int64(20261008 + i)
		g := newGenerator(seed)
		s0 := g.b.Build()
		s1, _ := g.mutate()
		runPair(t, seed, s0, s1, g.ops)
		_, err := ontology.Compare(s0, s1)
		if err != nil {
			statsErr++
		} else {
			statsOK++
		}
	}
	t.Logf("random pairs: %d ok, %d with expected errors", statsOK, statsErr)
}

// TestCrossLineageFallsBackToFullScan 验证不同谱系的快照对
// 走全量扫描路径，且结果与朴素模型一致。
func TestCrossLineageFallsBackToFullScan(t *testing.T) {
	for i := 0; i < 20; i++ {
		seed := int64(777000 + i)
		g := newGenerator(seed)
		s0 := g.b.Build()
		s1, basisBroken := g.mutate()
		if basisBroken {
			continue
		}
		// 通过 JSON 往返更换谱系标识。
		data, err := json.Marshal(s1)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var other ontology.Snapshot
		if err := json.Unmarshal(data, &other); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		other.Lineage = "OTHER"
		res, err := ontology.Compare(s0, &other)
		if err != nil {
			t.Fatalf("cross-lineage compare failed: %v", err)
		}
		if !res.Stats.FullScan {
			t.Fatalf("expected full scan for cross-lineage pair")
		}
		resNaive, err := naive.Compare(s0, &other)
		if err != nil {
			t.Fatalf("naive cross-lineage compare failed: %v", err)
		}
		if payloadOf(res) != payloadOf(resNaive) {
			t.Fatalf("cross-lineage mismatch with naive model, seed %d", seed)
		}
	}
}
