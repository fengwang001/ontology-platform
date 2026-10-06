package fontcore

import (
	"bytes"
	"errors"
	"log"
	"testing"
)

func newTestEngine(t *testing.T, cfg Config) *Engine {
	t.Helper()
	e, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var buf bytes.Buffer
	e.SetLogger(log.New(&buf, "[fc] ", log.Lmsgprefix))
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("engine log:\n%s", buf.String())
		}
	})
	return e
}

func rr0(lo, hi rune) RuneRange { return RuneRange{Lo: lo, Hi: hi} }

func face0(name string, wlo, whi int, st Style, wdlo, wdhi int,
	disp Display, ranges ...RuneRange) FaceSpec {
	return FaceSpec{Name: name, WeightLo: wlo, WeightHi: whi, Style: st,
		WidthLo: wdlo, WidthHi: wdhi, Display: disp, Ranges: ranges}
}

func mustReg(t *testing.T, e *Engine, spec FamilySpec) {
	t.Helper()
	if err := e.Register(spec); err != nil {
		t.Fatalf("Register %s: %v", spec.Name, err)
	}
}

func mustAdvance(t *testing.T, e *Engine, at int64) {
	t.Helper()
	if err := e.Advance(at); err != nil {
		t.Fatalf("Advance %d: %v", at, err)
	}
}

func shape0(t *testing.T, e *Engine, fam, text string, w, wd int, st Style) []CharResult {
	t.Helper()
	got, err := e.ShapeChars(fam, text, w, wd, st)
	if err != nil {
		t.Fatalf("ShapeChars: %v", err)
	}
	return got
}

// 宽度两侧偏向：请求宽于正常偏向更宽；窄于/等于正常偏向更窄。
func TestWidthBiasBothSides(t *testing.T) {
	e := newTestEngine(t, DefaultConfig())
	mustReg(t, e, FamilySpec{Name: "f", Faces: []FaceSpec{
		face0("n2", 400, 400, StyleNormal, 80, 100, DisplaySwap, rr0('a', 'z')),
		face0("w2", 400, 400, StyleNormal, 100, 120, DisplaySwap, rr0('a', 'z')),
	}})
	got := shape0(t, e, "f", "a", 400, 101, StyleNormal)
	if got[0].Face != "w2" {
		t.Fatalf("width=101 tie want wide w2, got %s", got[0].Face)
	}
	got = shape0(t, e, "f", "a", 400, 99, StyleNormal)
	if got[0].Face != "n2" {
		t.Fatalf("width=99 tie want narrow n2, got %s", got[0].Face)
	}
	got = shape0(t, e, "f", "a", 400, 100, StyleNormal)
	if got[0].Face != "n2" {
		t.Fatalf("width=100 want narrow bias, got %s", got[0].Face)
	}
}

// 斜体只允许正常 -> 斜体的单向合成。
func TestStyleSyntheticOneWay(t *testing.T) {
	e := newTestEngine(t, DefaultConfig())
	mustReg(t, e, FamilySpec{Name: "f", Faces: []FaceSpec{
		face0("it", 400, 400, StyleItalic, 100, 100, DisplaySwap, rr0('a', 'm')),
		face0("no", 400, 400, StyleNormal, 100, 100, DisplaySwap, rr0('n', 'z')),
	}})
	mustAdvance(t, e, 100)
	got := shape0(t, e, "f", "n", 400, 100, StyleItalic)
	if got[0].Face != "no" || !got[0].SyntheticItalic {
		t.Fatalf("synthetic from normal: %+v", got[0])
	}
	got = shape0(t, e, "f", "a", 400, 100, StyleNormal)
	if got[0].Face != "it" || got[0].SyntheticItalic {
		t.Fatalf("reverse synthetic forbidden: %+v", got[0])
	}
}

// 字重三段规则。
func TestWeightThreeBands(t *testing.T) {
	e := newTestEngine(t, DefaultConfig())
	mustReg(t, e, FamilySpec{Name: "f", Faces: []FaceSpec{
		face0("w100", 100, 100, StyleNormal, 100, 100, DisplaySwap, rr0('a', 'z')),
		face0("w300", 300, 300, StyleNormal, 100, 100, DisplaySwap, rr0('a', 'z')),
		face0("w500", 500, 500, StyleNormal, 100, 100, DisplaySwap, rr0('a', 'z')),
		face0("w700", 700, 700, StyleNormal, 100, 100, DisplaySwap, rr0('a', 'z')),
		face0("w900", 900, 900, StyleNormal, 100, 100, DisplaySwap, rr0('a', 'z')),
		face0("rng", 350, 450, StyleNormal, 100, 100, DisplaySwap, rr0('A', 'A')),
	}})
	cases := []struct {
		ch   rune
		w    int
		want string
	}{
		{'a', 200, "w100"},
		{'a', 350, "w300"},
		{'a', 450, "w500"}, // rng 不覆盖 'a'，故 500 先于 700
		{'a', 600, "w700"},
		{'a', 800, "w900"},
		{'A', 400, "rng"}, // 区间内命中
	}
	for _, c := range cases {
		got := shape0(t, e, "f", string(c.ch), c.w, 100, StyleNormal)
		if got[0].Face != c.want {
			t.Errorf("char %c weight %d: want %s got %s", c.ch, c.w, c.want, got[0].Face)
		}
	}
}

// 下阈值 == 上阈值：阈值两侧分别走低段与高段规则，不允许构造报错。
func TestWeightThresholdsEqual(t *testing.T) {
	cfg := DefaultConfig()
	cfg.WeightLow, cfg.WeightHigh = 400, 400
	e := newTestEngine(t, cfg)
	mustReg(t, e, FamilySpec{Name: "f", Faces: []FaceSpec{
		face0("w300", 300, 300, StyleNormal, 100, 100, DisplaySwap, rr0('a', 'b')),
		face0("w500", 500, 500, StyleNormal, 100, 100, DisplaySwap, rr0('a', 'b')),
	}})
	got := shape0(t, e, "f", "a", 399, 100, StyleNormal)
	if got[0].Face != "w300" {
		t.Fatalf("below want w300, got %s", got[0].Face)
	}
	got = shape0(t, e, "f", "a", 401, 100, StyleNormal)
	if got[0].Face != "w500" {
		t.Fatalf("above want w500, got %s", got[0].Face)
	}
	// 恰好等于公共阈值：低于/等于正常阈值时落入低段，先向下。
	got = shape0(t, e, "f", "a", 400, 100, StyleNormal)
	if got[0].Face != "w300" {
		t.Fatalf("equal thresholds boundary want w300, got %s", got[0].Face)
	}
	// 阈值颠倒必须拒绝。
	bad := DefaultConfig()
	bad.WeightLow, bad.WeightHigh = 500, 400
	if _, err := New(bad); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("reversed thresholds want ErrInvalidArgument, got %v", err)
	}
}

// 三维度完全并列取登记序最早者（范围不同但都覆盖目标字符，故允许共存）。
func TestFullTieRegistrationOrder(t *testing.T) {
	e := newTestEngine(t, DefaultConfig())
	mustReg(t, e, FamilySpec{Name: "f", Faces: []FaceSpec{
		face0("first", 400, 400, StyleNormal, 100, 100, DisplaySwap,
			rr0('a', 'm'), rr0('0', '9')),
		face0("second", 400, 400, StyleNormal, 100, 100, DisplaySwap,
			rr0('n', 'z'), rr0('a', 'm')),
	}})
	got := shape0(t, e, "f", "a", 400, 100, StyleNormal)
	if got[0].Face != "first" {
		t.Fatalf("tie: want first, got %s", got[0].Face)
	}
}

// 四维完全相同（含范围）视为重复登记并拒绝。
func TestExactDuplicateRejected(t *testing.T) {
	e := newTestEngine(t, DefaultConfig())
	mustReg(t, e, FamilySpec{Name: "f", Faces: []FaceSpec{
		face0("first", 400, 400, StyleNormal, 100, 100, DisplaySwap, rr0('a', 'z')),
	}})
	err := e.Register(FamilySpec{Name: "g", Faces: []FaceSpec{
		face0("x", 400, 400, StyleNormal, 100, 100, DisplaySwap, rr0('a', 'z')),
		face0("y", 400, 400, StyleNormal, 100, 100, DisplaySwap, rr0('a', 'z')),
	}})
	if !errors.Is(err, ErrDuplicateRegister) {
		t.Fatalf("want ErrDuplicateRegister, got %v", err)
	}
	// 被拒绝的操作不得改变登记。
	if _, ok := e.families["g"]; ok {
		t.Fatal("rejected registration must not create family")
	}
}

// 仅范围不同允许共存；字符只触发匹配胜者。
func TestRangesCoexistTriggerWinnerOnly(t *testing.T) {
	e := newTestEngine(t, DefaultConfig())
	mustReg(t, e, FamilySpec{Name: "f", Faces: []FaceSpec{
		face0("latin", 400, 400, StyleNormal, 100, 100, DisplaySwap, rr0('a', 'z')),
		face0("cjk", 400, 400, StyleNormal, 100, 100, DisplaySwap, rr0(0x4E00, 0x9FFF)),
	}})
	mustAdvance(t, e, 0)
	if _, err := e.Shape("f", "a中", 400, 100, StyleNormal); err != nil {
		t.Fatal(err)
	}
	if !e.loads[[2]string{"f", "latin"}].triggered {
		t.Fatal("latin should trigger")
	}
	if !e.loads[[2]string{"f", "cjk"}].triggered {
		t.Fatal("cjk should trigger")
	}
	e2 := newTestEngine(t, DefaultConfig())
	mustReg(t, e2, FamilySpec{Name: "g", Faces: []FaceSpec{
		face0("winner", 400, 400, StyleNormal, 100, 100, DisplaySwap, rr0('a', 'z')),
		face0("loser", 500, 500, StyleNormal, 100, 100, DisplaySwap, rr0('a', 'z')),
	}})
	shape0(t, e2, "g", "a", 400, 100, StyleNormal)
	if e2.loads[[2]string{"g", "loser"}].triggered {
		t.Fatal("loser must not trigger")
	}
}

var _ = errors.Is
