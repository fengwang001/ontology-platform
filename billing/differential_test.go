package billing

import (
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// 差分测试中的一次操作。两个实现在各自独立 RNG 之外共享完全相同的操作流。
type diffOp struct {
	kind      string
	slot      int
	inbound   int64
	outbound  int64
	version   int64
	committed int64
	tolerance int
}

type stepResult struct {
	errCode ErrorCode
	rate    int64
	set     *Settlement
	over    Overview
}

func runOp(b *Biller, op diffOp) stepResult {
	var r stepResult
	switch op.kind {
	case "submit":
		r.errCode = errCode(b.Submit(Sample{op.slot, op.inbound, op.outbound, op.version}))
	case "withdraw":
		r.errCode = errCode(b.Withdraw(op.slot, op.version))
	case "query":
		r.rate, r.errCode = errAndRate(b.CurrentRate())
	case "settle":
		r.set, r.errCode = errAndSettlement(b.Settle(op.committed, op.tolerance))
	case "overview":
		r.over = b.Overview()
	}
	return r
}

func runOpNaive(b *NaiveBiller, op diffOp) stepResult {
	var r stepResult
	switch op.kind {
	case "submit":
		r.errCode = errCode(b.Submit(Sample{op.slot, op.inbound, op.outbound, op.version}))
	case "withdraw":
		r.errCode = errCode(b.Withdraw(op.slot, op.version))
	case "query":
		r.rate, r.errCode = errAndRate(b.CurrentRate())
	case "settle":
		r.set, r.errCode = errAndSettlement(b.Settle(op.committed, op.tolerance))
	case "overview":
		r.over = b.Overview()
	}
	return r
}

func errAndRate(rate int64, err error) (int64, ErrorCode) { return rate, errCode(err) }

func errAndSettlement(s *Settlement, err error) (*Settlement, ErrorCode) { return s, errCode(err) }

func resultsEqual(a, b stepResult) bool {
	if a.errCode != b.errCode || a.rate != b.rate {
		return false
	}
	if (a.set == nil) != (b.set == nil) {
		return false
	}
	if a.set != nil && *a.set != *b.set {
		return false
	}
	if a.over != b.over {
		return false
	}
	return true
}

// decisionBasis 给出该操作结果的判定依据，使日志可人工复核。
func decisionBasis(op diffOp, r stepResult) string {
	if r.errCode != "" {
		return "rejected: " + string(r.errCode)
	}
	switch op.kind {
	case "submit":
		if r.errCode == "" {
			return "accepted: higher version overwrites / identical re-post is idempotent"
		}
		return "rejected: " + string(r.errCode)
	case "withdraw":
		if r.errCode == "" {
			return "accepted: current version matches; slot back to missing; version floor kept"
		}
		return "rejected: " + string(r.errCode)
	case "query":
		return fmt.Sprintf("rate=max after discarding floor(5%% of K): %d", r.rate)
	case "settle":
		return fmt.Sprintf("sealed: billed=%d base=%d valid=%d missing=%d repeated=%v",
			r.set.BilledRate, r.set.Base, r.set.ValidSlots, r.set.MissingSlots, r.set.Repeated)
	case "overview":
		return fmt.Sprintf("received=%d missing=%d rateDefined=%v rate=%d settled=%v",
			r.over.ReceivedSlots, r.over.MissingSlots, r.over.RateDefined, r.over.Rate, r.over.Settled)
	}
	return ""
}

func opString(op diffOp) string {
	switch op.kind {
	case "submit":
		return fmt.Sprintf("submit{slot=%d in=%d out=%d ver=%d}", op.slot, op.inbound, op.outbound, op.version)
	case "withdraw":
		return fmt.Sprintf("withdraw{slot=%d ver=%d}", op.slot, op.version)
	case "settle":
		return fmt.Sprintf("settle{committed=%d tolerance=%d}", op.committed, op.tolerance)
	default:
		return op.kind + "{}"
	}
}

func resultString(r stepResult) string {
	if r.errCode != "" {
		return "err=" + string(r.errCode)
	}
	switch {
	case r.set != nil:
		return fmt.Sprintf("settlement=%+v", *r.set)
	case r.over != (Overview{}):
		return fmt.Sprintf("overview=%+v", r.over)
	default:
		return fmt.Sprintf("rate=%d", r.rate)
	}
}

// genOps 用确定性 RNG 生成一条操作序列。版本号以小幅单调递增为主，
// 偶尔回退/重复，以触发过期、冲突与幂等路径。
func genOps(rng *rand.Rand, n int, steps int) []diffOp {
	ops := make([]diffOp, 0, steps)
	lastVersion := make([]int64, n)
	for i := range lastVersion {
		lastVersion[i] = 1
	}
	settleCount := 0
	for step := 0; step < steps; step++ {
		switch rng.Intn(100) {
		case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14:
			// 查询/概览只读，穿插检查一致性。
			if rng.Intn(2) == 0 {
				ops = append(ops, diffOp{kind: "query"})
			} else {
				ops = append(ops, diffOp{kind: "overview"})
			}
		case 15, 16:
			slot := rng.Intn(n)
			ops = append(ops, diffOp{kind: "withdraw", slot: slot, version: rng.Int63n(6) + 1})
		case 17:
			if settleCount < 2 {
				settleCount++
				ops = append(ops, diffOp{
					kind:      "settle",
					committed: rng.Int63n(MaxRate + 1),
					tolerance: rng.Intn(MaxTolerance + 1),
				})
			} else {
				step--
			}
		default:
			slot := rng.Intn(n)
			var v int64
			switch rng.Intn(10) {
			case 0:
				v = lastVersion[slot] // 完全同版本：可能幂等或冲突
			case 1:
				if lastVersion[slot] > 1 {
					v = lastVersion[slot] - 1 // 过期
				} else {
					v = 1
				}
			default:
				v = lastVersion[slot] + int64(rng.Intn(3)) // 更高版本（也可能重复当前+0）
			}
			if v < 1 {
				v = 1
			}
			lastVersion[slot] = v
			// 约 5% 的采样故意造越界，验证参数非法路径。
			var in, out int64
			if rng.Intn(20) == 0 {
				in = MaxRate + int64(rng.Intn(3)+1)
			} else {
				in = rng.Int63n(MaxRate + 1)
			}
			if rng.Intn(20) == 0 {
				out = -int64(rng.Intn(3) + 1)
			} else {
				out = rng.Int63n(MaxRate + 1)
			}
			ops = append(ops, diffOp{
				kind: "submit", slot: slot,
				inbound: in, outbound: out, version: v,
			})
		}
	}
	return ops
}

// TestRandomDifferential 1200 组随机操作序列与独立朴素模型逐步对照，
// 每步输入、输出与判定依据写入日志文件。
func TestRandomDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping differential test in -short mode")
	}
	const (
		sequences = 1200
		maxN      = 40
		maxSteps  = 120
	)

	logPath := filepath.Join(os.TempDir(), "billing-differential.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create log: %v", err)
	}
	defer logFile.Close()
	mw := io.MultiWriter(os.Stdout, logFile)

	totalSteps := 0
	for seq := 0; seq < sequences; seq++ {
		seed := int64(100000 + seq)
		rng := rand.New(rand.NewSource(seed))
		n := 1 + rng.Intn(maxN)
		steps := 30 + rng.Intn(maxSteps)
		ops := genOps(rng, n, steps)

		real, err := NewBiller(1_700_000_000, n)
		if err != nil {
			t.Fatalf("seq=%d NewBiller: %v", seq, err)
		}
		naive := NewNaiveBiller(1_700_000_000, n)

		fmt.Fprintf(mw, "===== sequence %d seed=%d n=%d steps=%d =====\n", seq, seed, n, len(ops))
		for i, op := range ops {
			totalSteps++
			rr := runOp(real, op)
			nr := runOpNaive(naive, op)
			fmt.Fprintf(mw, "seq=%d step=%d input=%s output{real=%s} output{naive=%s} basis=%s\n",
				seq, i, opString(op), resultString(rr), resultString(nr), decisionBasis(op, rr))
			if !resultsEqual(rr, nr) {
				t.Fatalf("sequence %d step %d diverged:\n  op=%s\n  real=%s\n  naive=%s\n(log: %s)",
					seq, i, opString(op), resultString(rr), resultString(nr), logPath)
			}
		}
	}
	t.Logf("differential OK: %d sequences, %d total steps; log=%s", sequences, totalSteps, logPath)
}
