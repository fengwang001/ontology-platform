package imagereclaim

import (
	"fmt"
	"math/rand"
	"os"
	"strings"
	"testing"
)

// TestRandomAgainstNaive 2000 组随机操作序列与朴素模拟逐步对照；
// 日志逐行打印输入、实际/朴素输出、两侧 used 与判定依据。
func TestRandomAgainstNaive(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping randomized differential test in short mode")
	}
	logFile, err := os.CreateTemp("", "imagereclaim-diff-*.log")
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	t.Logf("differential log (input/output/reason per op): %s", logFile.Name())

	const sequences = 2000
	mismatches := 0
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq + 1)))
		c, k, ops := generateOps(rng)
		var log strings.Builder
		fmt.Fprintf(&log, "===== sequence %d C=%d K=%d ops=%d =====\n", seq, c, k, len(ops))

		r, _ := New(c, k)
		n := newNaive(c, k)
		failAt := -1
		for i, op := range ops {
			var realRes GCResult
			var realErr error
			var want naiveOutcome
			switch op.kind {
			case opBegin:
				realErr = r.BeginPull(op.img, op.layers, op.now)
				want = n.begin(op.img, op.layers, op.now, false)
			case opCommit:
				realErr = r.CommitPull(op.img, op.now)
				want = n.commit(op.img, op.now)
			case opAbort:
				realErr = r.AbortPull(op.img, op.now)
				want = n.abort(op.img, op.now)
			case opPull:
				realErr = r.Pull(op.img, op.layers, op.now)
				want = n.begin(op.img, op.layers, op.now, true)
			case opRun:
				realErr = r.Run(op.img, op.now)
				want = n.run(op.img, op.now, false)
			case opStop:
				realErr = r.Stop(op.img, op.now)
				want = n.run(op.img, op.now, true)
			case opGC:
				realRes, realErr = r.GC(op.now, op.high, op.low, op.minAge)
				want = n.gc(op.now, op.high, op.low, op.minAge)
			}
			got := opResultFrom(realErr, realRes)

			reason := "agree"
			if !outcomeMatch(got, want) {
				reason = fmt.Sprintf("MISMATCH want=%s", formatNaive(want))
				failAt = i
			} else if got.errCode != 0 {
				reason = fmt.Sprintf("rejected code=%d layer=%q (state unchanged by both)", got.errCode, got.layerID)
			} else if op.kind == opGC {
				reason = fmt.Sprintf("GC deleted=%v freed=%d short=%d", realRes.Deleted, realRes.Freed, b2i(realRes.Short))
			}
			fmt.Fprintf(&log, "[%03d] %-44s real=%-38s naive=%-38s usedR=%d usedN=%d :: %s\n",
				i, describeOp(op), formatOutcome(got), formatNaive(want), r.Used(), n.usedNow(), reason)
			if failAt >= 0 || r.Used() != n.usedNow() {
				if failAt < 0 {
					failAt = i
					fmt.Fprintf(&log, "USED MISMATCH real=%d naive=%d\n", r.Used(), n.usedNow())
				}
				break
			}
		}

		snapR, snapN := fullSnapshot(r), naiveSnapshot(n)
		if snapR != snapN {
			fmt.Fprintf(&log, "STATE MISMATCH\n real: %s\nnaive: %s\n", snapR, snapN)
			if failAt < 0 {
				failAt = len(ops)
			}
		}
		if r.Used() < 0 || r.Used() > c {
			fmt.Fprintf(&log, "USED OUT OF RANGE used=%d C=%d\n", r.Used(), c)
			failAt = len(ops)
		}

		if failAt >= 0 {
			mismatches++
			logFile.WriteString(log.String())
			if mismatches <= 3 {
				t.Errorf("sequence %d diverged at op %d (see %s); tail:\n%s", seq, failAt, logFile.Name(), tailLines(log.String(), 12))
			}
			if mismatches > 10 {
				t.Fatalf("too many mismatches; see %s", logFile.Name())
			}
		} else if seq < 5 {
			logFile.WriteString(log.String())
		}
	}
	if mismatches > 0 {
		t.Fatalf("%d sequences mismatched; see %s", mismatches, logFile.Name())
	}
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
