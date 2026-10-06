package fontkernel

import (
	"bytes"
	"errors"
	"sync"
	"testing"
)

func testEngine() (*Engine, *bytes.Buffer) {
	var buf bytes.Buffer
	cfg := DefaultConfig()
	cfg.BlockBlockPeriod = 10
	cfg.SwapBlockPeriod = 1
	cfg.FallbackBlockPeriod = 2
	cfg.FallbackSwapPeriod = 3
	cfg.OptionalBlockPeriod = 2
	return New(cfg, &buf), &buf
}

func latin(a, b rune) []RuneRange { return []RuneRange{{Lo: a, Hi: b}} }

func basicFace(weight int, rng []RuneRange, p Policy) FaceSpec {
	return FaceSpec{
		WeightLo: weight, WeightHi: weight, WidthLo: 100, WidthHi: 100,
		Runes: rng, Policy: p, URL: "u",
	}
}

func widthFace(weight int, width float64, p Policy) FaceSpec {
	return FaceSpec{
		WeightLo: weight, WeightHi: weight, WidthLo: width, WidthHi: width,
		Runes: latin('a', 'z'), Policy: p, URL: "u",
	}
}

func TestWidthBiasBothSides(t *testing.T) {
	e, _ := testEngine()
	if err := e.RegisterFamily(FamilySpec{Name: "F", Faces: []FaceSpec{
		widthFace(400, 50, PolicySwap),
		widthFace(400, 200, PolicySwap),
	}}); err != nil {
		t.Fatal(err)
	}
	r, _ := e.Shape(ShapeRequest{Family: "F", Text: "a", Weight: 400, Width: 150})
	if r.Runes[0].WinnerFace != 1 {
		t.Fatalf("wide bias want face1 got %d", r.Runes[0].WinnerFace)
	}

	e2, _ := testEngine()
	_ = e2.RegisterFamily(FamilySpec{Name: "G", Faces: []FaceSpec{
		widthFace(400, 90, PolicySwap),
		widthFace(400, 10, PolicySwap),
	}})
	r, _ = e2.Shape(ShapeRequest{Family: "G", Text: "a", Weight: 400, Width: 50})
	if r.Runes[0].WinnerFace != 1 {
		t.Fatalf("narrow bias want face1 got %d", r.Runes[0].WinnerFace)
	}
}

func TestItalicOneWaySynthesis(t *testing.T) {
	e, _ := testEngine()
	_ = e.RegisterFamily(FamilySpec{Name: "F", Faces: []FaceSpec{basicFace(400, latin('a', 'z'), PolicySwap)}})
	r, _ := e.Shape(ShapeRequest{Family: "F", Text: "a", Weight: 400, Style: StyleItalic, Width: 100})
	if r.Runes[0].WinnerFace != 0 || !r.Runes[0].SyntheticItalic {
		t.Fatalf("normal face must synthesize italic: %+v", r.Runes[0])
	}

	e2, _ := testEngine()
	f := basicFace(400, latin('a', 'z'), PolicySwap)
	f.Style = StyleItalic
	_ = e2.RegisterFamily(FamilySpec{Name: "F", Faces: []FaceSpec{f}})
	r2, _ := e2.Shape(ShapeRequest{Family: "F", Text: "a", Weight: 400, Style: StyleNormal, Width: 100})
	if r2.Runes[0].WinnerFace != -1 || !r2.Runes[0].LastResort {
		t.Fatalf("reverse synthesis must be rejected: %+v", r2.Runes[0])
	}
}

func TestWeightThreeRegimesAndEqualThresholds(t *testing.T) {
	mk := func() *Engine {
		e, _ := testEngine()
		_ = e.RegisterFamily(FamilySpec{Name: "F", Faces: []FaceSpec{
			basicFace(300, latin('a', 'z'), PolicySwap),
			basicFace(600, latin('a', 'z'), PolicySwap),
			basicFace(450, latin('a', 'z'), PolicySwap),
		}})
		return e
	}
	for _, c := range []struct{ req, want int }{
		{300, 0},
		{200, 0},
		{700, 1},
		{400, 2},
		{470, 2},
		{500, 2},
	} {
		e := mk()
		r, _ := e.Shape(ShapeRequest{Family: "F", Text: "a", Weight: c.req, Width: 100})
		if r.Runes[0].WinnerFace != c.want {
			t.Errorf("weight %d want face %d got %d", c.req, c.want, r.Runes[0].WinnerFace)
		}
	}

	e, _ := testEngine()
	e.cfg.WeightLow, e.cfg.WeightHigh = 450, 450
	_ = e.RegisterFamily(FamilySpec{Name: "F", Faces: []FaceSpec{
		basicFace(400, latin('a', 'z'), PolicySwap),
		basicFace(500, latin('a', 'z'), PolicySwap),
	}})
	// 阈值恰等（450）：400 按“低于阈值先向下”命中自身；500 按“高于阈值先向上”命中自身。
	for _, c := range []struct{ req, want int }{{400, 0}, {500, 1}} {
		r, _ := e.Shape(ShapeRequest{Family: "F", Text: "a", Weight: c.req, Width: 100})
		if r.Runes[0].WinnerFace != c.want {
			t.Fatalf("equal thresholds req=%d want face%d got %d", c.req, c.want, r.Runes[0].WinnerFace)
		}
	}
	// 请求恰为重合阈值点本身、两侧均无精确命中时按中段“先向上”→ 500。
	r, _ := e.Shape(ShapeRequest{Family: "F", Text: "a", Weight: 450, Width: 100})
	if r.Runes[0].WinnerFace != 1 {
		t.Fatalf("at coalesced threshold want upward face1 got %d", r.Runes[0].WinnerFace)
	}

	bad := DefaultConfig()
	bad.WeightLow, bad.WeightHigh = 500, 400
	be := New(bad, nil)
	if err := be.RegisterFamily(FamilySpec{Name: "X", Faces: []FaceSpec{basicFace(400, latin('a', 'a'), PolicySwap)}}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("threshold inversion must be invalid: %v", err)
	}
}

func TestThreeWayTieGoesToEarliest(t *testing.T) {
	e, _ := testEngine()
	faces := []FaceSpec{
		{WeightLo: 300, WeightHi: 500, WidthLo: 90, WidthHi: 110, Runes: latin('a', 'z'), Policy: PolicySwap},
		{WeightLo: 350, WeightHi: 450, WidthLo: 95, WidthHi: 105, Runes: latin('a', 'z'), Policy: PolicySwap},
		{WeightLo: 400, WeightHi: 400, WidthLo: 100, WidthHi: 100, Runes: latin('a', 'z'), Policy: PolicySwap},
	}
	if err := e.RegisterFamily(FamilySpec{Name: "F", Faces: faces}); err != nil {
		t.Fatal(err)
	}
	r, _ := e.Shape(ShapeRequest{Family: "F", Text: "a", Weight: 400, Width: 100})
	if r.Runes[0].WinnerFace != 0 {
		t.Fatalf("fully tied candidates must resolve to earliest, got %d", r.Runes[0].WinnerFace)
	}
}

func TestCoverageOnlyFacesCoexistAndOnlyWinnerTriggers(t *testing.T) {
	e, _ := testEngine()
	if err := e.RegisterFamily(FamilySpec{Name: "F", Faces: []FaceSpec{
		basicFace(400, latin('a', 'm'), PolicySwap),
		basicFace(400, latin('n', 'z'), PolicySwap),
	}}); err != nil {
		t.Fatal(err)
	}
	r, _ := e.Shape(ShapeRequest{Family: "F", Text: "az", Weight: 400, Width: 100})
	if r.Runes[0].WinnerFace != 0 || r.Runes[1].WinnerFace != 1 {
		t.Fatalf("coverage winners wrong: %+v", r.Runes)
	}
	st := e.States("F")
	if st[0].Status != FaceLoading || st[0].TriggerAt != 0 || st[1].Status != FaceLoading {
		t.Fatalf("only covering faces trigger once: %+v", st)
	}
	_ = e.Advance(5)
	_, _ = e.Shape(ShapeRequest{Family: "F", Text: "a", Weight: 400, Width: 100})
	if st0 := e.States("F")[0]; st0.TriggerAt != 0 {
		t.Fatalf("trigger time must stay at first use, got %d", st0.TriggerAt)
	}

	e2, _ := testEngine()
	err := e2.RegisterFamily(FamilySpec{Name: "F", Faces: []FaceSpec{
		basicFace(400, latin('a', 'z'), PolicySwap),
		basicFace(400, latin('a', 'z'), PolicySwap),
	}})
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("identical faces must be rejected: %v", err)
	}
	if ss := e2.States("F"); len(ss) != 0 {
		t.Fatal("rejected registration must not mutate registry/state")
	}
}

func loadedFallback(t *testing.T, e *Engine, cover []RuneRange) {
	t.Helper()
	if err := e.RegisterFamily(FamilySpec{Name: "FB", Faces: []FaceSpec{basicFace(400, cover, PolicySwap)}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Shape(ShapeRequest{Family: "FB", Text: "a", Weight: 400, Width: 100}); err != nil {
		t.Fatal(err)
	}
	if err := e.ReportLoaded("FB", 0, 0); err != nil {
		t.Fatal(err)
	}
}

func TestPolicyBoundaryTransitions(t *testing.T) {
	type tc struct {
		name   string
		policy Policy
		block  int64
		swap   int64
	}
	cases := []tc{
		{"block", PolicyBlock, 10, -1},
		{"swap", PolicySwap, 1, -1},
		{"fallback", PolicyFallback, 2, 3},
		{"optional", PolicyOptional, 2, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, _ := testEngine()
			loadedFallback(t, e, latin('a', 'z'))
			_ = e.RegisterFamily(FamilySpec{Name: "F", Faces: []FaceSpec{basicFace(400, latin('a', 'z'), c.policy)}})

			shape := func() RuneResult {
				r, err := e.Shape(ShapeRequest{Family: "F", Fallback: []string{"FB"}, Text: "a", Weight: 400, Width: 100})
				if err != nil {
					t.Fatal(err)
				}
				return r.Runes[0]
			}
			if got := shape(); got.Period != PeriodBlock {
				t.Fatalf("t=0 want block got %d", got.Period)
			}
			if err := e.Advance(c.block); err != nil {
				t.Fatal(err)
			}
			atEnd := shape()
			if c.swap == 0 {
				if atEnd.Period != PeriodFallbackPermanent || atEnd.RenderFamily != "FB" {
					t.Fatalf("optional at block end: %+v", atEnd)
				}
			} else {
				if atEnd.Period != PeriodSwap || atEnd.RenderFamily != "FB" {
					t.Fatalf("%s at block end want swap/FB: %+v", c.name, atEnd)
				}
			}
			if c.swap > 0 {
				_ = e.Advance(c.block + c.swap)
				got := shape()
				if got.Period != PeriodFallbackPermanent || got.RenderFamily != "FB" {
					t.Fatalf("fallback at swap end: %+v", got)
				}
			}
			if c.swap < 0 {
				_ = e.Advance(c.block + 1000000)
				if got := shape(); got.Period != PeriodSwap {
					t.Fatalf("infinite swap expected: %+v", got)
				}
			}
		})
	}
}

func TestOptionalLateLoadDoesNotReplace(t *testing.T) {
	e, _ := testEngine()
	loadedFallback(t, e, latin('a', 'z'))
	_ = e.RegisterFamily(FamilySpec{Name: "F", Faces: []FaceSpec{basicFace(400, latin('a', 'z'), PolicyOptional)}})
	r, _ := e.Shape(ShapeRequest{Family: "F", Fallback: []string{"FB"}, Text: "a", Weight: 400, Width: 100})
	if r.Runes[0].Period != PeriodBlock {
		t.Fatalf("initial block: %+v", r.Runes[0])
	}
	_ = e.Advance(2)
	if err := e.ReportLoaded("F", 0, 3); !errors.Is(err, ErrInvalidLoadOp) {
		t.Fatalf("late load after give-up must be rejected, got %v", err)
	}
	r, _ = e.Shape(ShapeRequest{Family: "F", Fallback: []string{"FB"}, Text: "a", Weight: 400, Width: 100})
	if r.Runes[0].Period != PeriodFallbackPermanent || r.Runes[0].RenderFamily != "FB" {
		t.Fatalf("optional must stay on permanent fallback: %+v", r.Runes[0])
	}
	if st := e.States("F")[0]; st.Status != FaceFailed {
		t.Fatalf("optional timed-out face marked failed, got %d", st.Status)
	}
}

func TestLoadedFaceReplacesPlaceholder(t *testing.T) {
	e, _ := testEngine()
	_ = e.RegisterFamily(FamilySpec{Name: "F", Faces: []FaceSpec{basicFace(400, latin('a', 'z'), PolicyBlock)}})
	r, _ := e.Shape(ShapeRequest{Family: "F", Text: "a", Weight: 400, Width: 100})
	if r.Runes[0].Period != PeriodBlock {
		t.Fatalf("want block: %+v", r.Runes[0])
	}
	if err := e.ReportLoaded("F", 0, 5); err != nil {
		t.Fatal(err)
	}
	r, _ = e.Shape(ShapeRequest{Family: "F", Text: "a", Weight: 400, Width: 100})
	if r.Runes[0].Period != PeriodLoaded || r.Runes[0].RenderFamily != "F" {
		t.Fatalf("loaded face must replace placeholder: %+v", r.Runes[0])
	}
}

func TestFallbackNeverTriggersOwnLoading(t *testing.T) {
	e, _ := testEngine()
	_ = e.RegisterFamily(FamilySpec{Name: "F", Faces: []FaceSpec{basicFace(400, latin('a', 'z'), PolicySwap)}})
	_ = e.RegisterFamily(FamilySpec{Name: "FB", Faces: []FaceSpec{basicFace(400, latin('a', 'z'), PolicySwap)}})
	_, _ = e.Shape(ShapeRequest{Family: "F", Text: "a", Weight: 400, Width: 100}) // t=0 触发主脸
	_ = e.Advance(1)                                                              // 越过交换策略的极短阻塞期，主脸进入交换期
	r, _ := e.Shape(ShapeRequest{Family: "F", Fallback: []string{"FB"}, Text: "a", Weight: 400, Width: 100})
	if !r.Runes[0].LastResort {
		t.Fatalf("not-ready fallback must be skipped: %+v", r.Runes[0])
	}
	for _, s := range e.States("FB") {
		if s.Status != FaceUntriggered {
			t.Fatalf("fallback family must not be triggered as fallback: %+v", s)
		}
	}

	_, _ = e.Shape(ShapeRequest{Family: "FB", Text: "a", Weight: 400, Width: 100}) // 触发时刻为 1
	if err := e.ReportLoaded("FB", 0, 1); err != nil {
		t.Fatal(err)
	}
	r, _ = e.Shape(ShapeRequest{Family: "F", Fallback: []string{"FB"}, Text: "a", Weight: 400, Width: 100})
	if r.Runes[0].RenderFamily != "FB" || r.Runes[0].Period != PeriodSwap {
		t.Fatalf("ready fallback should render: %+v", r.Runes[0])
	}
}

func TestLastResortMarker(t *testing.T) {
	e, _ := testEngine()
	_ = e.RegisterFamily(FamilySpec{Name: "F", Faces: []FaceSpec{basicFace(400, latin('a', 'm'), PolicySwap)}})
	r, _ := e.Shape(ShapeRequest{Family: "F", Text: "z", Weight: 400, Width: 100})
	if !r.Runes[0].LastResort || r.Runes[0].RenderFamily != "" || r.Runes[0].RenderFace != -1 {
		t.Fatalf("uncovered rune must be last-resort: %+v", r.Runes[0])
	}
	if len(r.Segments) != 1 || !r.Segments[0].LastResort || r.Segments[0].Text != "z" {
		t.Fatalf("last-resort segment wrong: %+v", r.Segments)
	}
}

func TestSegmentMergeBoundaries(t *testing.T) {
	e, _ := testEngine()
	e.cfg.BlockBlockPeriod = 100 // face0 长阻塞，本测试结束时仍不可见
	loadedFallback(t, e, latin('a', 'z'))
	_ = e.RegisterFamily(FamilySpec{Name: "F", Faces: []FaceSpec{
		basicFace(400, latin('a', 'm'), PolicyBlock),
		basicFace(400, latin('n', 'z'), PolicySwap),
	}})
	_, _ = e.Shape(ShapeRequest{Family: "F", Text: "az", Weight: 400, Width: 100}) // t=0 触发 face0、face1
	_ = e.Advance(11)                                                              // face0 仍在长阻塞期；face1 已越过其 1 刻度阻塞期 → 交换 → FB
	r, _ := e.Shape(ShapeRequest{Family: "F", Fallback: []string{"FB"}, Text: "aaz", Weight: 400, Width: 100})
	if len(r.Segments) != 2 {
		t.Fatalf("want 2 segments got %d: %+v", len(r.Segments), r.Segments)
	}
	if r.Segments[0].Text != "aa" || r.Segments[0].Face != 0 || r.Segments[0].Period != PeriodBlock {
		t.Fatalf("segment0: %+v", r.Segments[0])
	}
	if r.Segments[1].Text != "z" || r.Segments[1].Family != "FB" {
		t.Fatalf("segment1: %+v", r.Segments[1])
	}
}

func TestErrorClassesAndRejectionOrder(t *testing.T) {
	e, _ := testEngine()
	bads := []FaceSpec{
		{WeightLo: 700, WeightHi: 400, WidthLo: 100, WidthHi: 100, Runes: latin('a', 'z'), Policy: PolicySwap},
		{WeightLo: 400, WeightHi: 400, WidthLo: 100, WidthHi: 100, Runes: nil, Policy: PolicySwap},
		{WeightLo: 400, WeightHi: 400, WidthLo: 100, WidthHi: 100, Runes: latin('a', 'z'), Policy: Policy(99)},
	}
	for i, b := range bads {
		if err := e.RegisterFamily(FamilySpec{Name: "B", Faces: []FaceSpec{b}}); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("bad spec %d: %v", i, err)
		}
	}
	_ = e.Advance(5)
	if err := e.Advance(4); !errors.Is(err, ErrClockRewound) {
		t.Fatalf("clock rewind: %v", err)
	}
	_ = e.RegisterFamily(FamilySpec{Name: "F", Faces: []FaceSpec{basicFace(400, latin('a', 'z'), PolicySwap)}})
	if _, err := e.Shape(ShapeRequest{Family: "NOPE", Text: "a", Weight: 400, Width: 100}); !errors.Is(err, ErrFamilyMissing) {
		t.Fatalf("family missing: %v", err)
	}
	if err := e.ReportLoaded("F", 9, 6); !errors.Is(err, ErrFaceMissing) {
		t.Fatalf("face missing precedes load-op: %v", err)
	}
	if err := e.ReportLoaded("NOPE", 0, 1); !errors.Is(err, ErrClockRewound) {
		t.Fatalf("rewind before family: %v", err)
	}
	if err := e.ReportLoaded("NOPE", 9, 6); !errors.Is(err, ErrFamilyMissing) {
		t.Fatalf("family before face: %v", err)
	}
	if err := e.ReportLoaded("F", 0, 6); !errors.Is(err, ErrInvalidLoadOp) {
		t.Fatalf("untriggered load op: %v", err)
	}
	if err := e.RegisterFamily(FamilySpec{Name: "F", Faces: []FaceSpec{basicFace(400, latin('a', 'z'), PolicySwap)}}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate family: %v", err)
	}
}

func TestRejectedOpsDoNotMutate(t *testing.T) {
	e, _ := testEngine()
	good := basicFace(400, latin('a', 'z'), PolicySwap)
	_ = e.RegisterFamily(FamilySpec{Name: "F", Faces: []FaceSpec{good}})
	_ = e.Advance(3)
	before := e.Now()
	_ = e.Advance(1)
	if e.Now() != before {
		t.Fatal("rejected advance moved clock")
	}
	bad := good
	bad.WeightLo, bad.WeightHi = 900, 100
	if err := e.RegisterFamily(FamilySpec{Name: "F", Faces: []FaceSpec{bad}}); err == nil {
		t.Fatal("expected reject")
	}
	if ss := e.States("F"); len(ss) != 1 {
		t.Fatalf("rejected register mutated state: %+v", ss)
	}
}

func TestConcurrentShapesConsistent(t *testing.T) {
	e, _ := testEngine()
	_ = e.RegisterFamily(FamilySpec{Name: "F", Faces: []FaceSpec{basicFace(400, latin('a', 'z'), PolicyBlock)}})
	req := ShapeRequest{Family: "F", Text: "abcabc", Weight: 400, Width: 100}
	var wg sync.WaitGroup
	results := make(chan *ShapeResult, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := e.Shape(req)
			if err != nil {
				t.Error(err)
				return
			}
			results <- r
		}()
	}
	wg.Wait()
	close(results)
	var ref []RuneResult
	for r := range results {
		if ref == nil {
			ref = r.Runes
			continue
		}
		if len(ref) != len(r.Runes) {
			t.Fatal("length mismatch under concurrency")
		}
		for i := range ref {
			if ref[i] != r.Runes[i] {
				t.Fatalf("concurrent shape divergence at %d: %+v vs %+v", i, ref[i], r.Runes[i])
			}
		}
	}
	st := e.States("F")
	if len(st) != 1 || st[0].TriggerAt != 0 || st[0].Status != FaceLoading {
		t.Fatalf("face must trigger exactly once: %+v", st)
	}
}

func TestLoggingContainsInputsOutputsAndReasoning(t *testing.T) {
	e, buf := testEngine()
	_ = e.RegisterFamily(FamilySpec{Name: "F", Faces: []FaceSpec{basicFace(400, latin('a', 'z'), PolicySwap)}})
	_, _ = e.Shape(ShapeRequest{Family: "F", Text: "a", Weight: 400, Width: 100})
	for _, want := range []string{"SHAPE input", "TRIGGER", "SHAPE rune", "SHAPE output"} {
		if !bytes.Contains(buf.Bytes(), []byte(want)) {
			t.Fatalf("log missing %q\n%s", want, buf.String())
		}
	}
}
