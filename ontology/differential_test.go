package ontology_test

import (
	"fmt"
	"math/rand"
	"testing"

	"ontology/naive"
	"ontology/ontology"
)

// TestDifferentialRandomOps 在随机操作序列下，将主实现的回放与审计结论
// 与独立朴素模型逐条对照。
func TestDifferentialRandomOps(t *testing.T) {
	for _, sym := range []bool{false, true} {
		name := "asymmetric"
		if sym {
			name = "symmetric"
		}
		t.Run(name, func(t *testing.T) { runDifferential(t, sym, 20261007) })
	}
}

func runDifferential(t *testing.T, sym bool, seed int64) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))

	store := ontology.NewStore()
	store.RegisterObjectType("A")
	store.RegisterObjectType("B")
	leftType, rightType := ontology.ObjectTypeID("A"), ontology.ObjectTypeID("B")
	if sym {
		rightType = "A"
	}
	lt := ontology.LinkTypeID("L")
	def := ontology.LinkTypeDef{ID: lt, LeftType: leftType, RightType: rightType, Symmetric: sym}
	if err := store.RegisterLinkType(def); err != nil {
		t.Fatal(err)
	}
	model := naive.NewModel(def)
	typeReady := store.Now()

	var objects []ontology.ObjectID
	for i := 0; i < 8; i++ {
		objects = append(objects, ontology.ObjectID(fmt.Sprintf("o%d", i)))
	}
	pick := func() ontology.ObjectID { return objects[rng.Intn(len(objects))] }
	pickDistinct := func() (ontology.ObjectID, ontology.ObjectID) {
		a, b := pick(), pick()
		for b == a {
			b = pick()
		}
		return a, b
	}

	// 随机操作序列：创建 / 撤销 / 补录 / 约束版本调整。
	const ops = 400
	for i := 0; i < ops; i++ {
		switch rng.Intn(10) {
		case 0, 1, 2, 3: // create
			a, b := pickDistinct()
			src := ontology.Endpoint(rng.Intn(3))
			vf := int64(rng.Intn(50))
			if err := store.RecordFact(lt, ontology.FactInput{
				Kind: ontology.FactCreate, Left: a, Right: b, ValidFrom: vf, Source: src,
			}); err != nil {
				t.Fatal(err)
			}
			model.Apply(naive.Fact{
				RecordedAt: store.Now(), Kind: ontology.FactCreate,
				A: a, B: b, ValidFrom: vf, EndorsedBoth: src == ontology.EndpointBoth,
			})
		case 4, 5, 6: // revoke
			a, b := pickDistinct()
			vto := int64(rng.Intn(50))
			if err := store.RecordFact(lt, ontology.FactInput{
				Kind: ontology.FactRevoke, Left: a, Right: b, ValidTo: vto,
				Source: ontology.Endpoint(rng.Intn(3)),
			}); err != nil {
				t.Fatal(err)
			}
			model.Apply(naive.Fact{
				RecordedAt: store.Now(), Kind: ontology.FactRevoke, A: a, B: b, ValidTo: vto,
			})
		case 7: // corroborate（仅对称类型有意义，对非对称类型为空操作）
			a, b := pickDistinct()
			if err := store.RecordFact(lt, ontology.FactInput{
				Kind: ontology.FactCorroborate, Left: a, Right: b, Source: ontology.EndpointRight,
			}); err != nil {
				t.Fatal(err)
			}
			model.Apply(naive.Fact{
				RecordedAt: store.Now(), Kind: ontology.FactCorroborate, A: a, B: b,
			})
		case 8: // 约束版本调整
			left := ontology.Cardinality{Min: 0, Max: rng.Intn(4) - 1}
			right := ontology.Cardinality{Min: 0, Max: rng.Intn(4) - 1}
			if _, err := store.AdjustCardinality(lt, left, right); err != nil {
				t.Fatal(err)
			}
			model.AdjustCardinality(store.Now(), left, right)
		default: // 空转一拍，拉开记录时刻
			store.RegisterObjectType(ontology.ObjectTypeID(fmt.Sprintf("pad-%d", i)))
		}
	}
	now := store.Now()

	// 对照一：随机 (rt, vt) 回放结果逐条一致（含缺损标记）。
	for q := 0; q < 300; q++ {
		rt := typeReady + rng.Int63n(now-typeReady+1)
		vt := int64(rng.Intn(60))
		got := store.Replay(lt, rt, vt)
		wantLinks, wantDeficit := model.LinksAt(rt, vt)
		if len(got.Links) != len(wantLinks) {
			t.Fatalf("rt=%d vt=%d: link count %d != naive %d", rt, vt, len(got.Links), len(wantLinks))
		}
		for i, wl := range wantLinks {
			gl := got.Links[i]
			if gl.Left != wl[0] || gl.Right != wl[1] {
				t.Fatalf("rt=%d vt=%d: link %d = (%s,%s), naive = (%s,%s)",
					rt, vt, i, gl.Left, gl.Right, wl[0], wl[1])
			}
			_, deficit := wantDeficit[wl]
			if gl.SymmetryDeficit != deficit {
				t.Fatalf("rt=%d vt=%d: deficit of (%s,%s) = %v, naive = %v",
					rt, vt, wl[0], wl[1], gl.SymmetryDeficit, deficit)
			}
		}
	}

	// 对照二：全区间审计的逐记录时刻违反集合与朴素模型一致。
	latestVersion := len(model.Versions)
	rep, err := store.Audit(ontology.AuditRequest{
		LinkType: lt, RecordFrom: typeReady, RecordTo: now,
		ValidAt: 25, ExpectedVersion: latestVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Err != nil && rep.Err.Class != ontology.ErrorMirrorInconsistency {
		t.Fatalf("unexpected audit error: %+v", rep.Err)
	}
	if rep.Err != nil {
		// 存在单端记录缺损时审计报告镜像不一致；朴素模型确认缺损确实存在。
		_, deficits := model.LinksAt(now, 25)
		if !sym || len(deficits) == 0 {
			t.Fatalf("mirror inconsistency reported but naive model found no deficit")
		}
		return
	}
	// 由分段结果重建每个记录时刻的违反集合，与朴素模型逐时刻对照。
	violatorAt := func(rt int64) map[ontology.ObjectID]int {
		out := map[ontology.ObjectID]int{}
		for _, seg := range rep.Segments {
			for _, v := range seg.Violations {
				if v.From <= rt && rt < v.To {
					out[v.Object] = v.Degree
				}
			}
		}
		return out
	}
	for rt := typeReady; rt <= now; rt++ {
		got := violatorAt(rt)
		want := model.ViolatorsAt(rt, 25)
		if len(got) != len(want) {
			t.Fatalf("rt=%d: violators %v != naive %v", rt, got, want)
		}
		for obj, d := range want {
			if got[obj] != d {
				t.Fatalf("rt=%d: degree of %s = %d, naive = %d", rt, obj, got[obj], d)
			}
		}
	}
	// 对照三：审计分段边界与朴素模型的版本序列一致。
	naiveBounds := []int64{typeReady}
	for _, v := range model.Versions {
		if v.EffectiveFrom > typeReady && v.EffectiveFrom <= now {
			naiveBounds = append(naiveBounds, v.EffectiveFrom)
		}
	}
	if len(rep.Segments) != len(naiveBounds) {
		t.Fatalf("segment count %d != naive %d", len(rep.Segments), len(naiveBounds))
	}
	for i, seg := range rep.Segments {
		if seg.From != naiveBounds[i] || seg.Version != i+1 {
			t.Fatalf("segment %d = %+v, naive bound %d", i, seg, naiveBounds[i])
		}
	}
}
