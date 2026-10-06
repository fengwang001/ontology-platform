package fontcore

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// 错误类别与拒绝次序。
func TestErrorClassesAndOrder(t *testing.T) {
	bad := testCfg()
	bad.WeightLow, bad.WeightHigh = 500, 400
	if _, err := New(bad); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("threshold reversed -> invalid, got %v", err)
	}
	e := newTestEngine(t, testCfg())
	mustReg(t, e, FamilySpec{Name: "f", Faces: []FaceSpec{
		face0("p", 400, 400, StyleNormal, 100, 100, DisplaySwap, rr0('a', 'z')),
	}})
	if _, err := e.ShapeChars("ghost", "a", -1, 100, StyleNormal); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid precedes family: %v", err)
	}
	if err := e.Advance(5); err != nil {
		t.Fatal(err)
	}
	if err := e.Advance(4); !errors.Is(err, ErrClockRewound) {
		t.Fatalf("rewind: %v", err)
	}
	if err := e.Loaded("ghost", "p"); !errors.Is(err, ErrFamilyNotFound) {
		t.Fatalf("family: %v", err)
	}
	if err := e.Loaded("f", "ghost"); !errors.Is(err, ErrFaceNotFound) {
		t.Fatalf("face: %v", err)
	}
	if err := e.Loaded("f", "p"); !errors.Is(err, ErrInvalidLoadState) {
		t.Fatalf("untriggered loaded: %v", err)
	}
	if err := e.Failed("f", "p"); !errors.Is(err, ErrInvalidLoadState) {
		t.Fatalf("untriggered failed: %v", err)
	}
	specs := []FamilySpec{
		{Name: "b1", Faces: []FaceSpec{
			face0("x", 700, 400, StyleNormal, 100, 100, DisplaySwap, rr0('a', 'z'))}},
		{Name: "b2", Faces: []FaceSpec{
			face0("x", 400, 400, StyleNormal, 100, 50, DisplaySwap, rr0('a', 'z'))}},
		{Name: "b3", Faces: []FaceSpec{
			face0("x", 400, 400, StyleNormal, 100, 100, DisplaySwap)}},
		{Name: "b4", Faces: []FaceSpec{
			face0("x", 400, 400, StyleNormal, 100, 100, Display(99), rr0('a', 'z'))}},
	}
	for _, s := range specs {
		if err := e.Register(s); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("spec %s want invalid, got %v", s.Name, err)
		}
		if _, ok := e.families[s.Name]; ok {
			t.Fatalf("rejected register %s changed state", s.Name)
		}
	}
	if _, err := e.ShapeChars("ghost", "a", 400, 100, StyleNormal); !errors.Is(err, ErrFamilyNotFound) {
		t.Fatalf("shape missing family: %v", err)
	}
	before := e.Now()
	_ = e.Advance(before - 1)
	if e.Now() != before {
		t.Fatal("rejected advance changed clock")
	}
}

func errClass(err error) string {
	switch {
	case err == nil:
		return "nil"
	case errors.Is(err, ErrInvalidArgument):
		return "invalid"
	case errors.Is(err, ErrClockRewound):
		return "rewind"
	case errors.Is(err, ErrFamilyNotFound):
		return "nofam"
	case errors.Is(err, ErrFaceNotFound):
		return "noface"
	case errors.Is(err, ErrDuplicateRegister):
		return "dup"
	case errors.Is(err, ErrInvalidLoadState):
		return "state"
	default:
		return "other"
	}
}

func charsEqual(a, b []CharResult) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// querySteps 统计区间树查询经历的节点数，用于可验证的复杂度论证。
func (t0 *rangeTree) querySteps(r rune) int {
	n, steps := t0.root, 0
	for n != nil {
		steps++
		if r < n.x {
			n = n.lo
		} else if r > n.x {
			n = n.hi
		} else {
			break
		}
	}
	return steps
}

// TestPerformanceNotLinear 可验证地说明两项复杂度要求：
//   - 单字符匹配只遍历覆盖候选；构造 64 张互不覆盖人脸，候选恒为 1；
//   - 覆盖判定走区间树，树高随段数对数增长（直接统计查询节点数）。
func TestPerformanceNotLinear(t *testing.T) {
	var faces []FaceSpec
	for i := 0; i < 64; i++ {
		base := rune('a' + i*4)
		faces = append(faces, face0(fmt.Sprintf("f%d", i), 400, 400,
			StyleNormal, 100, 100, DisplaySwap, RuneRange{base, base + 3}))
	}
	e := newTestEngine(t, testCfg())
	mustReg(t, e, FamilySpec{Name: "big", Faces: faces})
	fm := e.families["big"]
	for i := 0; i < 64; i++ {
		r := rune('a' + i*4)
		if ids := fm.coveringIDs(r, nil); len(ids) != 1 {
			t.Fatalf("rune %c: want exactly 1 covering face, got %d", r, len(ids))
		}
	}
	for _, n := range []int{16, 64, 256, 1024} {
		var rss []RuneRange
		for i := 0; i < n; i++ {
			lo := rune(1 + i*10)
			rss = append(rss, RuneRange{lo, lo + 1})
		}
		tree := newRangeTree(rss)
		steps := tree.querySteps(rune(2))
		// 上界 log2(n)+1；允许中位端点近似带来的常数偏差。
		bound := 0
		for v := n; v > 1; v >>= 1 {
			bound++
		}
		if steps > bound+2 {
			t.Fatalf("n=%d steps=%d exceeds log bound %d", n, steps, bound+2)
		}
	}
}

var diffChars = []rune("ab中文字体xy0123")

type diffWorld struct {
	eng *Engine
	nav *naiveModel
	rng *rand.Rand
	log []string
}

func joinLog(l []string) string { return strings.Join(l, "\n") }

func buildDiffWorld(t *testing.T, seed int64) *diffWorld {
	t.Helper()
	cfg := testCfg()
	eng, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	w := &diffWorld{eng: eng, nav: newNaiveModel(cfg),
		rng: rand.New(rand.NewSource(seed))}
	w.spawnFamily("fb2", nil)
	w.spawnFamily("fb1", []string{"fb2"})
	w.spawnFamily("main", []string{"fb1", "fb2"})
	return w
}

func (w *diffWorld) spawnFamily(name string, fbs []string) {
	displays := []Display{DisplayBlock, DisplaySwap, DisplayFallback, DisplayOptional}
	n := 2 + w.rng.Intn(4)
	var faces []FaceSpec
	seen := map[string]bool{}
	for i := 0; i < n; i++ {
		fn := fmt.Sprintf("%s-f%d", name, i)
		wlo := []int{100, 300, 400, 500, 700, 900}[w.rng.Intn(6)]
		whi := wlo
		if w.rng.Intn(3) == 0 && wlo < 900 {
			whi = wlo + 100
		}
		style := Style(w.rng.Intn(2))
		wc := []int{75, 100, 125}[w.rng.Intn(3)]
		var rngs []RuneRange
		blocks := []RuneRange{{'a', 'z'}, {'0', '9'}, {0x4E00, 0x4E20}}
		for _, b := range blocks {
			if w.rng.Intn(2) == 0 {
				rngs = append(rngs, b)
			}
		}
		if len(rngs) == 0 {
			rngs = blocks[:1]
		}
		spec := face0(fn, wlo, whi, style, wc, wc,
			displays[w.rng.Intn(len(displays))], rngs...)
		key := fmt.Sprintf("%d|%d|%d|%d|%v", wlo, whi, style, wc, mergeRanges(rngs))
		if seen[key] {
			// 仅范围不同即可共存：补一个独立区间保证四维不全同。
			spec.Ranges = append(spec.Ranges, RuneRange{rune(0x5000 + i), rune(0x5000 + i)})
			key = fmt.Sprintf("%s|%x", key, 0x5000+i)
		}
		seen[key] = true
		faces = append(faces, spec)
	}
	fs := FamilySpec{Name: name, Faces: faces, Fallbacks: fbs}
	if err := w.eng.Register(fs); err != nil {
		panic(fmt.Sprintf("eng register %s: %v", name, err))
	}
	if err := w.nav.register(fs); err != nil {
		panic(fmt.Sprintf("nav register %s: %v", name, err))
	}
}

func (w *diffWorld) randomText() string {
	n := 1 + w.rng.Intn(6)
	runes := make([]rune, n)
	for i := range runes {
		runes[i] = diffChars[w.rng.Intn(len(diffChars))]
	}
	return string(runes)
}

func reportOrSkipped(e *Engine, fam, face string, loaded bool) error {
	if _, ok := e.families[fam]; !ok {
		return ErrFamilyNotFound
	}
	if _, ok := e.families[fam].byName[face]; !ok {
		return ErrFaceNotFound
	}
	if loaded {
		return e.Loaded(fam, face)
	}
	return e.Failed(fam, face)
}

// TestRandomDifferential 用随机操作序列对照引擎与独立朴素模型：
// 整形结果、错误类别、触发状态与触发时刻必须始终一致。
func TestRandomDifferential(t *testing.T) {
	for seed := int64(0); seed < 80; seed++ {
		w := buildDiffWorld(t, seed)
		var clock int64
		for step := 0; step < 300; step++ {
			switch w.rng.Intn(10) {
			case 0, 1:
				clock += int64(w.rng.Intn(7))
				w.log = append(w.log, fmt.Sprintf("advance %d", clock))
				if err := w.eng.Advance(clock); err != nil {
					t.Fatalf("seed=%d eng advance: %v", seed, err)
				}
				if err := w.nav.advance(clock); err != nil {
					t.Fatalf("seed=%d nav advance: %v", seed, err)
				}
			case 2, 3, 4, 5:
				text := w.randomText()
				weight := []int{100, 300, 400, 450, 500, 600, 900}[w.rng.Intn(7)]
				width := []int{75, 100, 125}[w.rng.Intn(3)]
				style := Style(w.rng.Intn(2))
				w.log = append(w.log, fmt.Sprintf("shape main %q w=%d wd=%d it=%d",
					text, weight, width, style))
				got, gerr := w.eng.ShapeChars("main", text, weight, width, style)
				want := w.nav.shape("main", text, shapeReq{weight, width, style})
				if gerr != nil {
					t.Fatalf("seed=%d step=%d shape: %v", seed, step, gerr)
				}
				if !charsEqual(got, want) {
					t.Fatalf("seed=%d step=%d MISMATCH text=%q w=%d wd=%d it=%d\neng=%+v\nnav=%+v\nops:\n%s",
						seed, step, text, weight, width, style, got, want, joinLog(w.log))
				}
			case 6, 7, 8:
				fam := []string{"main", "fb1", "fb2"}[w.rng.Intn(3)]
				fname := fmt.Sprintf("%s-f%d", fam, w.rng.Intn(6))
				loaded := w.rng.Intn(2) == 0
				w.log = append(w.log, fmt.Sprintf("report %s/%s loaded=%v", fam, fname, loaded))
				e1 := reportOrSkipped(w.eng, fam, fname, loaded)
				e2 := w.nav.report(fam, fname, loaded)
				if errClass(e1) != errClass(e2) {
					t.Fatalf("seed=%d step=%d report %s/%s loaded=%v: eng=%v nav=%v",
						seed, step, fam, fname, loaded, e1, e2)
				}
			default:
				fam := []string{"fb1", "fb2"}[w.rng.Intn(2)]
				text := w.randomText()
				w.log = append(w.log, fmt.Sprintf("shape %s %q", fam, text))
				if _, err := w.eng.ShapeChars(fam, text, 400, 100, StyleNormal); err != nil {
					t.Fatal(err)
				}
				_ = w.nav.shape(fam, text, shapeReq{400, 100, StyleNormal})
			}
			for _, fam := range []string{"main", "fb1", "fb2"} {
				for fi := 0; fi < 6; fi++ {
					fname := fmt.Sprintf("%s-f%d", fam, fi)
					ld, ok := w.eng.loads[[2]string{fam, fname}]
					nf := w.nav.families[fam]
					if nf == nil {
						continue
					}
					nl, ok2 := nf.loads[fname]
					if ok != ok2 {
						t.Fatalf("face existence mismatch %s/%s", fam, fname)
					}
					if ok && (ld.triggered != nl.triggered ||
						(ld.triggered && ld.triggerAt != nl.triggerAt)) {
						t.Fatalf("seed=%d trigger mismatch %s/%s: eng=%+v nav=%+v",
							seed, fam, fname, ld, nl)
					}
				}
			}
		}
	}
}
