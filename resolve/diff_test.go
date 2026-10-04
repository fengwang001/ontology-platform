package resolve

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

// TestRandomDifferential 用 1500 组随机操作序列将正式实现与朴素模拟器逐步对照，
// 同时校验：每记录恰输出一次、待定数 <= Pmax、墙钟/来源/次序一致。
func TestRandomDifferential(t *testing.T) {
	const trials = 1500
	for trial := 0; trial < trials; trial++ {
		rng := rand.New(rand.NewSource(int64(trial*7919 + 1)))
		log := runDifferential(t, rng, trial)
		if t.Failed() {
			t.Logf("trial %d log:\n%s", trial, log)
			return
		}
	}
}

func runDifferential(t *testing.T, rng *rand.Rand, trial int) string {
	t.Helper()
	r := New()
	n := newNaive()
	const nDev = 2
	for d := int64(0); d < nDev; d++ {
		pmax := 1 + rng.Intn(20)
		if err := r.Register(d, pmax); err != nil {
			t.Fatal(err)
		}
		if err := n.Register(d, pmax); err != nil {
			t.Fatal(err)
		}
	}
	// 每设备各自的“当前最大 boot”用于生成合法/非法序号。
	maxBoot := make([]int64, nDev)
	emitted := map[string]bool{}
	var recSeq int64
	log := fmt.Sprintf("=== trial %d ===\n", trial)

	const nOps = 120
	for i := 0; i < nOps; i++ {
		dev := int64(rng.Intn(nDev))
		var o nOp
		if maxBoot[dev] == 0 || rng.Intn(10) < 5 {
			maxBoot[dev]++
		}
		cur := maxBoot[dev]
		// 以大概率用当前 boot，小概率随机旧/新序号（制造 sealed/跳号）。
		b := cur
		switch rng.Intn(10) {
		case 0:
			if cur > 1 {
				b = rng.Int63n(cur) + 1
			}
		case 1:
			b = cur + int64(rng.Intn(3))
		}
		if rng.Intn(10) < 6 {
			o = nOp{kind: 'R', dev: dev, b: b, k: rng.Int63n(200)}
			recSeq++
			o.payload = fmt.Sprintf("t%d_d%d_%d", trial, dev, recSeq)
		} else {
			o = nOp{kind: 'S', dev: dev, b: b, k: rng.Int63n(200), w: rng.Int63n(100000)}
			if rng.Intn(20) == 0 {
				o.k = -1 // 偶发非法参数
			}
		}

		var got []Emit
		var gerr error
		if o.kind == 'R' {
			got, gerr = r.Record(o.dev, o.b, o.k, o.payload)
		} else {
			got, gerr = r.Sync(o.dev, o.b, o.k, o.w)
		}
		want, nerr, reason := n.step(o)

		log += fmt.Sprintf("op %d: %s\n", i, n.logOp(o))
		log += fmt.Sprintf("    naive: err=%v reason=%s emits=%s\n", errName(nerr), reason, formatNaive(want))
		log += fmt.Sprintf("    impl:  err=%v emits=%s\n", errName(gerr), formatNaive(emitsToNaive(got)))

		if errorKind(gerr) != errorKind(nerr) {
			t.Fatalf("trial %d op %d %s: err impl=%v naive=%v", trial, i, n.logOp(o), gerr, nerr)
		}
		if gerr == nil {
			if b > maxBoot[dev] {
				maxBoot[dev] = b
			}
			gs := emitsToNaive(got)
			if len(gs) != len(want) {
				t.Fatalf("trial %d op %d: emit count %d vs %d", trial, i, len(gs), len(want))
			}
			for j := range gs {
				if gs[j] != want[j] {
					t.Fatalf("trial %d op %d emit[%d]: impl=%+v naive=%+v", trial, i, j, gs[j], want[j])
				}
				if emitted[gs[j].payload] {
					t.Fatalf("trial %d: payload %q emitted twice", trial, gs[j].payload)
				}
				emitted[gs[j].payload] = true
			}
			if d := r.devs[dev]; d.pending > d.pmax {
				t.Fatalf("trial %d op %d: pending %d > pmax %d", trial, i, d.pending, d.pmax)
			}
		}
	}
	return log
}

func errorKind(err error) string { return errName(err) }

func errName(err error) string {
	switch {
	case err == nil:
		return "nil"
	case errors.Is(err, ErrInvalid):
		return "ErrInvalid"
	case errors.Is(err, ErrNoDevice):
		return "ErrNoDevice"
	case errors.Is(err, ErrSealed):
		return "ErrSealed"
	case errors.Is(err, ErrDupSync):
		return "ErrDupSync"
	case errors.Is(err, ErrSkew):
		return "ErrSkew"
	case errors.Is(err, ErrFull):
		return "ErrFull"
	default:
		return err.Error()
	}
}

// TestDifferentialLogSample 在 -v 下打印一组序列的输入/输出/判定依据示例。
func TestDifferentialLogSample(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	log := runDifferential(t, rng, 99999)
	t.Log("\n" + log)
}
