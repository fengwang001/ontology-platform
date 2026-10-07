package temporal

import (
	"context"
	"fmt"
	"math/rand"
	"testing"
)

// diffWorld 是一个随机操作序列生成器，对 Engine 与 NaiveEngine 同步施加，
// 逐条对照错误码与任意时刻的访问判定。
type diffWorld struct {
	t     *testing.T
	rng   *rand.Rand
	eng   *Engine
	naive *NaiveEngine
	tick  Tick
	seq   int
	live  map[string]Change // 两边共同已接受且未撤回的变更
}

func newDiffWorld(t *testing.T, seed int64) *diffWorld {
	w := &diffWorld{
		t:     t,
		rng:   rand.New(rand.NewSource(seed)),
		eng:   NewEngine(0, nil),
		naive: NewNaiveEngine(0),
		live:  map[string]Change{},
	}
	return w
}

func (w *diffWorld) sl() (Subject, Label) {
	return Subject(fmt.Sprintf("u%d", w.rng.Intn(3))),
		Label(fmt.Sprintf("l%d", w.rng.Intn(2)))
}

func (w *diffWorld) newID(prefix string) string {
	w.seq++
	return fmt.Sprintf("%s%d", prefix, w.seq)
}

func (w *diffWorld) run(steps int) {
	ctx := context.Background()
	for i := 0; i < steps; i++ {
		roll := w.rng.Intn(100)
		switch {
		case roll < 45:
			w.genGrantDeny(ctx)
		case roll < 65:
			w.genRevoke(ctx)
		case roll < 80:
			w.genWithdraw(ctx)
		default:
			w.genQuery(ctx)
		}
	}
	// 收尾：对所有出现过的时刻做一次全量交叉核对。
	for at := Tick(0); at <= 60; at++ {
		for s := 0; s < 3; s++ {
			for l := 0; l < 2; l++ {
				w.assertDecide(ctx, Subject(fmt.Sprintf("u%d", s)),
					Label(fmt.Sprintf("l%d", l)), at)
			}
		}
	}
}

func (w *diffWorld) genGrantDeny(ctx context.Context) {
	commit := Tick(w.rng.Intn(40))
	eff := commit + Tick(w.rng.Intn(10)) // 保证一部分同刻、一部分延迟生效
	// 约 1/8 概率故意制造“生效早于提交”，验证双方一致拒绝。
	if w.rng.Intn(8) == 0 {
		eff = commit - 1
		if eff < 0 {
			eff = -1
			commit = 2
		}
	}
	s, l := w.sl()
	c := Change{
		ID:        w.newID("g"),
		Subject:   s,
		Label:     l,
		Kind:      []Kind{Grant, Deny}[w.rng.Intn(2)],
		Committed: commit,
		Effective: eff,
	}
	// 约 1/4 的概率挂上一个同主体标签的现存依赖。
	if dep, ok := w.pickDependency(s, l); ok && w.rng.Intn(4) == 0 && eff >= commit {
		c.DependsOn = dep
	}
	r1, e1 := w.eng.Submit(ctx, c)
	r2, e2 := w.naive.Submit(ctx, c)
	w.compareErrors("submit", c.ID, e1, e2)
	if e1 == nil {
		w.live[c.ID] = c
		_ = r1
		_ = r2
	}
}

func (w *diffWorld) genRevoke(ctx context.Context) {
	commit := Tick(w.rng.Intn(45))
	candidates := []Change{}
	for _, c := range w.live {
		if c.Kind != Revoke && c.Effective <= commit && c.Committed <= commit {
			candidates = append(candidates, c)
		}
	}
	if len(candidates) == 0 {
		w.genQuery(ctx)
		return
	}
	target := candidates[w.rng.Intn(len(candidates))]
	c := Change{
		ID:        w.newID("r"),
		Subject:   target.Subject,
		Label:     target.Label,
		Kind:      Revoke,
		Committed: commit,
		Effective: commit + Tick(w.rng.Intn(8)),
		Target:    target.ID,
	}
	if w.rng.Intn(8) == 0 {
		c.Effective = c.Committed - 1 // 触发优先级 1 错误
	}
	r1, e1 := w.eng.Submit(ctx, c)
	_, e2 := w.naive.Submit(ctx, c)
	w.compareErrors("revoke", c.ID, e1, e2)
	if e1 == nil {
		w.live[c.ID] = c
		// 校验重新考察结论的自洽性：每个被点名的变更，其 StillValid
		// 必须等于撤销生效时刻重放后的存活状态。
		if r1 != nil {
			bucket := w.eng.index[c.Subject][c.Label]
			aliveNow := replay(visibleAt(bucket, c.Effective)).alive
			for _, ra := range r1.Reassessment {
				alive := aliveNow[ra.ChangeID]
				if alive != ra.StillValid {
					w.t.Fatalf("reassessment inconsistency for %s: claim=%v replay=%v",
						ra.ChangeID, ra.StillValid, alive)
				}
			}
		}
	}
}

func (w *diffWorld) genWithdraw(ctx context.Context) {
	at := Tick(w.rng.Intn(50))
	candidates := []Change{}
	for _, c := range w.live {
		if c.Effective > at {
			candidates = append(candidates, c)
		}
	}
	var id string
	if len(candidates) > 0 {
		id = candidates[w.rng.Intn(len(candidates))].ID
	} else {
		id = "missing"
	}
	e1 := w.eng.Withdraw(ctx, id, at)
	e2 := w.naive.Withdraw(ctx, id, at)
	w.compareErrors("withdraw", id, e1, e2)
	if e1 == nil {
		delete(w.live, id)
	}
}

func (w *diffWorld) genQuery(ctx context.Context) {
	s, l := w.sl()
	at := Tick(w.rng.Intn(60) - 2) // 允许早于 horizon 的查询
	w.assertDecide(ctx, s, l, at)
}

func (w *diffWorld) assertDecide(ctx context.Context, s Subject, l Label, at Tick) {
	d1, e1 := w.eng.Decide(ctx, s, l, at)
	d2, e2 := w.naive.Decide(ctx, s, l, at)
	w.compareErrors(fmt.Sprintf("decide %s/%s@%d", s, l, at), "", e1, e2)
	if e1 != nil {
		return
	}
	if d1.Allowed != d2.Allowed || d1.Default != d2.Default ||
		d1.Examined != d2.Examined || !sameBasis(d1.Basis, d2.Basis) {
		w.t.Fatalf("decision mismatch %s/%s@%d: engine=%+v naive=%+v",
			s, l, at, d1, d2)
	}
}

func (w *diffWorld) pickDependency(s Subject, l Label) (string, bool) {
	ids := []string{}
	for _, c := range w.live {
		if c.Subject == s && c.Label == l && c.Kind != Revoke {
			ids = append(ids, c.ID)
		}
	}
	if len(ids) == 0 {
		return "", false
	}
	return ids[w.rng.Intn(len(ids))], true
}

func (w *diffWorld) compareErrors(op, id string, e1, e2 error) {
	c1 := ErrCodeNone
	c2 := ErrCodeNone
	if de, ok := AsDomainError(e1); ok {
		c1 = de.Code
	}
	if de, ok := AsDomainError(e2); ok {
		c2 = de.Code
	}
	if c1 != c2 {
		w.t.Fatalf("%s %s error code mismatch: engine=%s naive=%s",
			op, id, c1, c2)
	}
}

func sameBasis(a, b []string) bool {
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

func TestRandomDifferential(t *testing.T) {
	for seed := int64(1); seed <= 40; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			newDiffWorld(t, seed).run(400)
		})
	}
}
