package consistency

import (
	"context"
	"fmt"
	"math/rand/v2"
	"testing"
)

// TestRandomParityWithNaiveReference 用确定性随机序列同时驱动有限历史的
// Store 与保留全历史的朴素参照，每一步（含拒绝）都比较错误类别与快照。
func TestRandomParityWithNaiveReference(t *testing.T) {
	ctx := context.Background()
	viewNames := []string{"alpha", "beta", "gamma"}

	for _, seed := range []uint64{1, 7, 42, 99, 2026} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
			retain := 1 + int(rng.IntN(4))
			s := mustStore(t, retain, viewNames...)
			ref := newNaive(retain, viewNames...)

			progress := make(map[string]Timestamp, len(viewNames))
			for _, v := range viewNames {
				progress[v] = -1
			}

			step := 0
			for range 3000 {
				step++
				view := viewNames[rng.IntN(len(viewNames))]
				roll := rng.IntN(100)

				switch {
				case roll < 55: // 应用：70% 合法前进，30% 故意非法/不前进
					var ts Timestamp
					if rng.IntN(10) < 7 {
						ts = progress[view] + 1 + rng.Int64N(3)
					} else {
						switch rng.IntN(3) {
						case 0:
							ts = progress[view] // 相等 -> not-advancing
						case 1:
							ts = progress[view] - rng.Int64N(3) // 倒退
						default:
							ts = -rng.Int64N(3) - 1 // 负数 -> invalid
						}
					}
					var value Value
					if ts >= 0 && rng.IntN(20) == 0 {
						value = nil // 故意触发 invalid
					} else {
						value = fmt.Sprintf("%s@%d", view, ts)
					}

					gotErr := s.Apply(ctx, view, ts, value)
					wantErr := ref.apply(view, ts, value)
					assertClass(t, step, "apply", gotErr, wantErr)
					if gotErr == nil {
						progress[view] = ts
					}

				case roll < 70: // 心跳
					var ts Timestamp
					if rng.IntN(10) < 7 {
						ts = progress[view] + 1 + rng.Int64N(3)
					} else {
						ts = progress[view] - rng.Int64N(2)
					}
					gotErr := s.Heartbeat(ctx, view, ts)
					wantErr := ref.heartbeat(view, ts)
					assertClass(t, step, "heartbeat", gotErr, wantErr)
					if gotErr == nil {
						progress[view] = ts
					}

				case roll < 92: // 指定点读取：多数点围绕窗口，少量越界/负数
					at := pickReadTime(rng, ref, viewNames)
					got, gotErr := s.ReadAt(ctx, at)
					want, wantErr := ref.readAt(at)
					assertClass(t, step, "readAt", gotErr, wantErr)
					if gotErr == nil {
						if !snapshotsEqual(got, want) {
							t.Fatalf("step %d readAt(%d) mismatch:\n got=%+v\nwant=%+v", step, at, got, want)
						}
					}

				default: // 自动单调读取
					got, gotErr := s.Read(ctx)
					want, wantErr := ref.read()
					assertClass(t, step, "read", gotErr, wantErr)
					if gotErr == nil && !snapshotsEqual(got, want) {
						t.Fatalf("step %d read mismatch:\n got=%+v\nwant=%+v", step, got, want)
					}
				}
			}
		})
	}
}

func pickReadTime(rng *rand.Rand, ref *naiveStore, views []string) Timestamp {
	if rng.IntN(8) == 0 {
		return -rng.Int64N(3) - 1 // 故意非法
	}
	// 在 0..某最大进度 之间取点，自然覆盖“未准备好/太旧/可读”三类。
	maxProgress := Timestamp(-1)
	for _, v := range views {
		if p := ref.views[v].progress; p > maxProgress {
			maxProgress = p
		}
	}
	if maxProgress < 0 {
		return rng.Int64N(3)
	}
	return rng.Int64N(maxProgress + 4)
}

func assertClass(t *testing.T, step int, op string, got, want error) {
	t.Helper()
	if errClass(got) != errClass(want) {
		t.Fatalf("step %d %s class=%s want %s (got err=%v)", step, op, errClass(got), errClass(want), got)
	}
}

// TestMonotonicAndReproducible 校验连续 Read 单调不减，且同一时间点
// 在仍可读期间逐视图结果不变。
func TestMonotonicAndReproducible(t *testing.T) {
	ctx := context.Background()
	s := mustStore(t, 100, "a", "b")

	for ts := int64(1); ts <= 20; ts++ {
		must(t, s.Apply(ctx, "a", ts, fmt.Sprintf("a%d", ts)))
		must(t, s.Apply(ctx, "b", ts, fmt.Sprintf("b%d", ts)))

		snap, err := s.Read(ctx)
		if err != nil {
			t.Fatalf("step %d read: %v", ts, err)
		}
		if snap.At != ts {
			t.Fatalf("step %d at=%d want %d", ts, snap.At, ts)
		}
	}

	// 同点重读：ts=10 仍在保留窗口内，结果必须与首次读取一致。
	snap, err := s.ReadAt(ctx, 10)
	if err != nil {
		t.Fatalf("reread 10: %v", err)
	}
	if err := snapExpect(snap, map[string]Value{"a": "a10", "b": "b10"}); err != nil {
		t.Fatal(err)
	}

	// Read 的游标只进不退；只推进 a 时 minProgress 不变，Read 必须返回同一点。
	must(t, s.Apply(ctx, "a", 21, "a21"))
	stuck, err := s.Read(ctx)
	if err != nil {
		t.Fatalf("read while lagging b: %v", err)
	}
	if stuck.At != 20 {
		t.Fatalf("Read at=%d want 20 (must not jump past min progress)", stuck.At)
	}

	// 淘汰到游标之后，Read 仍返回最新一致点（3），且结果必须真实可读。
	tiny := mustStore(t, 2, "a", "b")
	must(t, tiny.Apply(ctx, "a", 1, "a1"))
	must(t, tiny.Apply(ctx, "b", 1, "b1"))
	first, err := tiny.Read(ctx)
	if err != nil || first.At != 1 {
		t.Fatalf("first read at=%d err=%v want 1", first.At, err)
	}
	must(t, tiny.Apply(ctx, "a", 2, "a2"))
	must(t, tiny.Apply(ctx, "b", 2, "b2"))
	must(t, tiny.Apply(ctx, "a", 3, "a3"))
	must(t, tiny.Apply(ctx, "b", 3, "b3")) // 1 被淘汰
	next, err := tiny.Read(ctx)
	if err != nil {
		t.Fatalf("read after eviction: %v", err)
	}
	if next.At != 3 {
		t.Fatalf("read after eviction at=%d want 3", next.At)
	}
	if err := snapExpect(next, map[string]Value{"a": "a3", "b": "b3"}); err != nil {
		t.Fatal(err)
	}
}
