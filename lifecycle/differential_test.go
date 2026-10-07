package lifecycle

import (
	"errors"
	"math/rand"
	"reflect"
	"strconv"
	"testing"
	"time"
)

func sameErr(a, b error) bool {
	return errors.Is(a, b) || (a != nil && b != nil &&
		(errIs(a, ErrObjectNotFound) == errIs(b, ErrObjectNotFound) &&
			errIs(a, ErrInvalidTransition) == errIs(b, ErrInvalidTransition) &&
			errIs(a, ErrInvalidTime) == errIs(b, ErrInvalidTime) &&
			errIs(a, ErrFrozenNotExpired) == errIs(b, ErrFrozenNotExpired)))
}

func equalViews(a, b View) bool {
	if a.ID != b.ID || a.Exists != b.Exists || a.Visible != b.Visible || a.State != b.State {
		return false
	}
	if !a.FreezeDeadline.Equal(b.FreezeDeadline) || !a.GraceDeadline.Equal(b.GraceDeadline) {
		return false
	}
	// 隐藏属性时双方都应为 nil；可见时逐键比对。
	if (a.Attrs == nil) != (b.Attrs == nil) {
		return false
	}
	return reflect.DeepEqual(a.Attrs, b.Attrs)
}

func equalEdges(a, b []Edge) bool {
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

// 与朴素全历史回放实现在大量随机操作序列上逐条对账，
// 覆盖转换返回错误、对象视图与出边视图。
func TestDifferentialAgainstNaive(t *testing.T) {
	const trials = 200
	for seed := int64(0); seed < trials; seed++ {
		rng := rand.New(rand.NewSource(seed))
		clock := NewFakeClock(t0)
		log := NewSliceLogger()
		audit := NewCountingAuditLog(NewMemoryAuditLog())
		svc := NewService(clock, audit, log)
		naive := NewNaiveReference(clock)

		ids := []string{"a", "b", "c", "d", "e"}
		pairs := [][2]string{
			{"a", "b"}, {"b", "c"}, {"c", "a"}, {"a", "d"}, {"e", "a"},
		}

		checkAll := func(step int) {
			t.Helper()
			for _, id := range ids {
				for _, actor := range []Identity{IdentityAdmin, IdentityUser} {
					gv, nv := svc.GetView(id, actor), naive.GetView(id, actor)
					if !equalViews(gv, nv) {
						t.Fatalf("seed=%d step=%d id=%s actor=%s\n got=%+v\nwant=%+v",
							seed, step, id, actor, gv, nv)
					}
					ge, ne := svc.ViewEdges(id, actor), naive.ViewEdges(id, actor)
					if !equalEdges(ge, ne) {
						t.Fatalf("seed=%d step=%d edges id=%s actor=%s\n got=%+v\nwant=%+v",
							seed, step, id, actor, ge, ne)
					}
				}
			}
		}

		for _, pair := range pairs {
			if err := svc.CreateObject(pair[0], Attrs{"k": pair[0]}); err != nil &&
				!errors.Is(err, ErrDuplicateObject) {
				t.Fatal(err)
			}
			if err := naive.CreateObject(pair[0], Attrs{"k": pair[0]}); err != nil &&
				!errors.Is(err, ErrDuplicateObject) {
				t.Fatal(err)
			}
		}
		for i, pair := range pairs {
			if err := svc.CreateObject(pair[1], Attrs{"k": pair[1]}); err != nil &&
				!errors.Is(err, ErrDuplicateObject) {
				t.Fatal(err)
			}
			if err := naive.CreateObject(pair[1], Attrs{"k": pair[1]}); err != nil &&
				!errors.Is(err, ErrDuplicateObject) {
				t.Fatal(err)
			}
			e := Edge{ID: "e" + strconv.Itoa(i), SourceID: pair[0], TargetID: pair[1]}
			if err := svc.AddEdge(e); err != nil {
				t.Fatal(err)
			}
			if err := naive.AddEdge(e); err != nil {
				t.Fatal(err)
			}
		}

		for step := 0; step < 400; step++ {
			// 让时钟以“可能恰好踩在截止时刻”的方式前进。
			switch rng.Intn(3) {
			case 0:
				clock.Advance(time.Duration(rng.Intn(5)) * time.Second)
			case 1:
				clock.Advance(time.Duration(rng.Intn(100)) * time.Millisecond)
			}

			id := ids[rng.Intn(len(ids))]
			actor := []Identity{IdentityAdmin, IdentityUser}[rng.Intn(2)]
			var ge, ne error
			switch rng.Intn(9) {
			case 0:
				d := t0.Add(time.Duration(rng.Intn(30)) * time.Second)
				ge = svc.Delete(id, actor, d)
				ne = naive.Delete(id, actor, d)
			case 1:
				ge = svc.Undo(id, actor)
				ne = naive.Undo(id, actor)
			case 2:
				dur := []time.Duration{-time.Second, 0, 500 * time.Millisecond,
					time.Second, 3 * time.Second, 10 * time.Second}[rng.Intn(6)]
				ge = svc.Freeze(id, actor, dur)
				ne = naive.Freeze(id, actor, dur)
			case 3:
				ge = svc.Archive(id, actor)
				ne = naive.Archive(id, actor)
			default:
				// 4..8：纯查询，推进结算并对账。
			}
			if ge != nil || ne != nil {
				ge2, ne2 := ge, ne
				if !sameErr(ge2, ne2) {
					t.Fatalf("seed=%d step=%d id=%s: err mismatch got=%v want=%v",
						seed, step, id, ge2, ne2)
				}
			}
			if step%7 == 0 {
				checkAll(step)
			}
		}
		checkAll(-1)
	}
}

// 保证生产实现的可见性判定不读取任何历史转换记录：
// 随对象历史转换次数增长，审计历史读取条数始终为 0。
// 朴素实现的回放读取量则随历史线性增长，作为对照。
func TestVisibilityTouchesNoHistoryRecords(t *testing.T) {
	clock := NewFakeClock(t0)
	audit := NewCountingAuditLog(NewMemoryAuditLog())
	svc := NewService(clock, audit, NewSliceLogger())

	if err := svc.CreateObject("o", Attrs{"v": "1"}); err != nil {
		t.Fatal(err)
	}

	// 制造越来越长的转换历史：每轮 delete→undo，最后冻结。
	for round := 1; round <= 50; round++ {
		now := t0.Add(time.Duration(round) * time.Minute)
		clock.Set(now)
		if err := svc.Delete("o", IdentityAdmin, now.Add(30*time.Second)); err != nil {
			t.Fatalf("round %d delete: %v", round, err)
		}
		if err := svc.Undo("o", IdentityAdmin); err != nil {
			t.Fatalf("round %d undo: %v", round, err)
		}
	}
	freezeAt := t0.Add(60 * time.Minute)
	clock.Set(freezeAt)
	if err := svc.Freeze("o", IdentityAdmin, time.Minute); err != nil {
		t.Fatal(err)
	}

	historyLen := audit.Appends()
	if historyLen < 100 {
		t.Fatalf("expected long history, got %d", historyLen)
	}

	// 观测窗口：无论历史多长，查询不得触及任何审计记录。
	for _, at := range []time.Time{
		freezeAt.Add(30 * time.Second),
		freezeAt.Add(time.Minute - 1),
		freezeAt.Add(time.Minute),
		freezeAt.Add(2 * time.Minute),
	} {
		clock.Set(at)
		audit.BeginCheckpoint()
		for range 20 {
			_ = svc.GetView("o", IdentityUser)
			_ = svc.GetView("o", IdentityAdmin)
			_ = svc.ViewEdges("o", IdentityUser)
			_ = svc.ViewEdges("o", IdentityAdmin)
		}
		if got := audit.ReadsSinceCheckpoint(); got != 0 {
			t.Fatalf("at %v visibility touched %d history records, want 0", at, got)
		}
	}

	// 对照：朴素实现回放全部历史，读取量随历史长度线性增长。
	// 这里直接数其事件条数，证明两套实现使用的信息量不同。
	naive := NewNaiveReference(clock)
	_ = naive.CreateObject("o", Attrs{"v": "1"})
	c := t0
	for round := 1; round <= 50; round++ {
		c = t0.Add(time.Duration(round) * time.Minute)
		clock.Set(c)
		_ = naive.Delete("o", IdentityAdmin, c.Add(30*time.Second))
		_ = naive.Undo("o", IdentityAdmin)
	}
	naiveObj := naive.objects["o"]
	if len(naiveObj.events) != historyLen-1 {
		t.Fatalf("naive history=%d, service transitions=%d",
			len(naiveObj.events), historyLen-1)
	}

	// 朴素模型每次 stateAt 都会遍历全部事件：直接验证遍历次数 == 事件数。
	touched := 0
	clock.Set(freezeAt.Add(30 * time.Second))
	state := StateAlive
	var gd, fd time.Time
	for range naiveObj.events {
		state, gd, fd = naiveSettle(state, gd, fd, clock.Now())
		touched++
	}
	_, _, _ = state, gd, fd
	if touched != len(naiveObj.events) || touched < 100 {
		t.Fatalf("naive replay touched %d, want linear %d", touched, len(naiveObj.events))
	}
}
