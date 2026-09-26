package hpi

import (
	"errors"
	"fmt"
	"math/big"

	"ontology/hp"
)

// ErrSelfCheck 是自检失败的可判定哨兵错误（%w 包裹具体原因）。
var ErrSelfCheck = errors.New("hpi: self-check failed")

func fail(f string, a ...any) error { return fmt.Errorf("%w: %s", ErrSelfCheck, fmt.Sprintf(f, a...)) }

// SelfCheck 复核内置五步序列与冗余预判边数上界，再校验当前区域四条不变量。
func (r *Region) SelfCheck() error {
	if err := builtInSequence(); err != nil {
		return err
	}
	if err := edgeCountBounded(); err != nil {
		return err
	}
	vs := r.Verts()
	r.mu.RLock()
	hps := append([]hp.HalfPlane(nil), r.hps...)
	r.mu.RUnlock()
	return checkInvariants(vs, hps)
}

func builtInSequence() error {
	r := New()
	hs := []hp.HalfPlane{
		{A: -1, B: 0, C: 0}, {A: 0, B: -1, C: 0}, {A: 1, B: 1, C: 6},
		{A: 1, B: 0, C: 4}, {A: 0, B: 1, C: 4},
	}
	want := [][][2]int64{
		{{0, -Bound}, {Bound, -Bound}, {Bound, Bound}, {0, Bound}},
		{{0, 0}, {Bound, 0}, {Bound, Bound}, {0, Bound}},
		{{0, 0}, {6, 0}, {0, 6}},
		{{0, 0}, {4, 0}, {4, 2}, {0, 6}},
		{{0, 0}, {4, 0}, {4, 2}, {2, 4}, {0, 4}},
	}
	for i, h := range hs {
		_ = r.Add(h) // 内置半平面均合法，不会失败
		got := r.Verts()
		if len(got) != len(want[i]) {
			return fail("step %d: want %d verts, got %d", i+1, len(want[i]), len(got))
		}
		for j, w := range want[i] {
			if !hp.Eq(got[j], hp.Pt(w[0], w[1])) {
				return fail("step %d vert %d mismatch", i+1, j)
			}
		}
	}
	return nil
}

func edgeCountBounded() error {
	r := New()
	box := []hp.HalfPlane{
		{A: -1, B: 0, C: 0}, {A: 0, B: -1, C: 0}, {A: 1, B: 0, C: 1}, {A: 0, B: 1, C: 1},
	}
	for i := 0; i < 500; i++ {
		box = append(box, hp.HalfPlane{A: 1 + int64(i%20), B: 1 + int64(i/20), C: 10000})
	}
	for i, h := range box {
		if err := r.Add(h); err != nil {
			return err
		}
		if i >= 4 && r.edgesChecked != 0 {
			return fail("redundant add %d walked %d edges", i, r.edgesChecked)
		}
	}
	return nil
}

// checkInvariants 校验给定区域快照的四条不变量。
func checkInvariants(vs []hp.Point, hps []hp.HalfPlane) error {
	naive := New().Verts() // 不变量 1：不走包围盒捷径的朴素逐次裁剪
	for _, h := range hps {
		naive = clip(naive, h, new(int))
	}
	if len(vs) == 0 {
		if len(naive) >= 3 {
			return fail("empty region but naive replay has %d verts", len(naive))
		}
		return gridCheck(hps, nil)
	}
	if len(naive) != len(vs) {
		return fail("naive replay verts %d != %d", len(naive), len(vs))
	}
	for i := range vs {
		if !hp.Eq(naive[i], vs[i]) {
			return fail("naive replay mismatch at %d", i)
		}
		for _, h := range hps { // 不变量 2：顶点满足全部半平面（含边界）
			if hp.Side(h, vs[i]).Sign() > 0 {
				return fail("vert %d violates half-plane", i)
			}
		}
		j := (i + 1) % len(vs) // 不变量 3：无重复、CCW、凸
		ex, ey := new(big.Rat).Sub(vs[j].X, vs[i].X), new(big.Rat).Sub(vs[j].Y, vs[i].Y)
		if ex.Sign() == 0 && ey.Sign() == 0 {
			return fail("duplicate vert at %d", i)
		}
		for k := 2; k < len(vs); k++ {
			w := vs[(i+k)%len(vs)]
			if cross2(ex, ey, new(big.Rat).Sub(w.X, vs[i].X), new(big.Rat).Sub(w.Y, vs[i].Y)).Sign() < 0 {
				return fail("non-convex/CCW at edge %d", i)
			}
		}
	}
	return gridCheck(hps, vs) // 不变量 2（外部）：区域外的点必违反某个半平面
}

func cross2(ax, ay, bx, by *big.Rat) *big.Rat {
	return new(big.Rat).Sub(new(big.Rat).Mul(ax, by), new(big.Rat).Mul(ay, bx))
}

func gridCheck(hps []hp.HalfPlane, vs []hp.Point) error {
	violates := func(q hp.Point) bool {
		for _, h := range hps {
			if hp.Side(h, q).Sign() > 0 {
				return true
			}
		}
		return false
	}
	for gx := -Bound; gx <= Bound; gx += Bound / 4 {
		for gy := -Bound; gy <= Bound; gy += Bound / 4 {
			if q := hp.Pt(gx, gy); (len(vs) == 0 || !insideConvex(vs, q)) && !violates(q) {
				return fail("outside point (%d,%d) satisfies all half-planes", gx, gy)
			}
		}
	}
	return nil
}

func insideConvex(vs []hp.Point, q hp.Point) bool {
	for i := range vs {
		j := (i + 1) % len(vs)
		if cross2(new(big.Rat).Sub(vs[j].X, vs[i].X), new(big.Rat).Sub(vs[j].Y, vs[i].Y),
			new(big.Rat).Sub(q.X, vs[i].X), new(big.Rat).Sub(q.Y, vs[i].Y)).Sign() < 0 {
			return false
		}
	}
	return true
}
