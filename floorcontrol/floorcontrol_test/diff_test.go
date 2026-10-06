package floorcontrol_test

import (
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	fc "ontology/floorcontrol"
)

const diffSequences = 1500

// genSequence 生成一条随机、时间单调（偶发非法/回退）的操作序列。
func genSequence(rng *rand.Rand) (int64, int, []Op) {
	s := int64(1 + rng.IntN(20))
	q := 1 + rng.IntN(6)
	users := []string{"u0", "u1", "u2", "u3", "u4", "u5"}
	n := 40 + rng.IntN(120)
	ops := make([]Op, 0, n)
	var now int64

	pick := func() string { return users[rng.IntN(len(users))] }
	target := func() string {
		if rng.IntN(8) == 0 {
			return "ghost"
		}
		return pick()
	}

	kinds := []string{
		"Join", "Join", "Leave", "Raise", "Raise", "Lower",
		"Appoint", "Dismiss", "Grant", "Yield", "Mute", "Unmute", "Snapshot", "QueuePos",
	}
	for range n {
		kind := kinds[rng.IntN(len(kinds))]
		t := now + int64(1+rng.IntN(int(s)+4))
		opNow := t
		op := Op{Kind: kind, A: pick(), Now: opNow}
		switch kind {
		case "Appoint", "Dismiss", "Mute", "Unmute":
			op.B = target()
		case "Snapshot":
			op.A = ""
		case "QueuePos":
			op.Now = now // QueuePos 不使用 now，保持单调仅为可读性
		}
		advance := true
		switch rng.IntN(40) {
		case 0:
			// 时钟回退：时间戳回到过去，不推进共享时钟。
			op.Now = now - int64(1+rng.IntN(int(s)+2))
			advance = false
		case 1:
			// 参数非法（空标识），时间戳保持为合法的未来时刻。
			if kind != "Snapshot" {
				op.A = ""
				op.Now = now
				advance = false
			}
		}
		if advance {
			now = op.Now
		}
		ops = append(ops, op)
	}
	return s, q, ops
}

func runDifferential(t *testing.T, s int64, q int, ops []Op, logAll bool) string {
	t.Helper()
	real, err := fc.New(s, q)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	nav := newNaive(s, q)

	var log strings.Builder
	fmt.Fprintf(&log, "config S=%d Q=%d ops=%d\n", s, q, len(ops))

	for i, op := range ops {
		ro := runReal(real, op)
		no := runNaive(nav, op)
		reason, equal := outcomesEqual(ro, no)
		fmt.Fprintf(&log, "#%03d %-32s => real=%-18q naive=%-18q\n",
			i, op.String(), errName(ro.err), errName(no.err))
		if op.Kind == "Snapshot" && ro.err == nil && no.err == nil {
			fmt.Fprintf(&log, "       real: %s\n      naive: %s\n",
				oKindView(ro.snap), oKindView(no.snap))
		}
		if !equal {
			fmt.Fprintf(&log, "MISMATCH: %s\n", reason)
			t.Fatalf("sequence mismatch at #%d %s: %s\n%s", i, op.String(), reason, log.String())
		}
		// 生产模型结构不变量（即使是被拒绝操作后也必须成立）。
		checkInvariants(t, real, op)
	}
	fmt.Fprintf(&log, "VERDICT: identical across both models\n")
	if logAll {
		t.Logf("\n%s", log.String())
	}
	return log.String()
}

func checkInvariants(t *testing.T, r *fc.Room, after Op) {
	t.Helper()
	s, err := r.Snapshot(after.NowOrLast())
	if err != nil {
		if err == fc.ErrClosed || err == fc.ErrClockBack || err == fc.ErrInvalidArgument {
			return
		}
		t.Fatalf("invariant snapshot after %s: %v", after, err)
	}
	seen := map[string]bool{}
	for _, u := range s.Queue {
		if seen[u] {
			t.Fatalf("duplicate queue member %q after %s", u, after)
		}
		seen[u] = true
		if u == s.Speaker {
			t.Fatalf("speaker %q also in queue after %s", u, after)
		}
	}
}

func TestRandomDifferential(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "differential.log")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	t.Logf("full differential log: %s", path)

	for seq := 0; seq < diffSequences; seq++ {
		rng := rand.New(rand.NewPCG(uint64(seq+1), uint64(0x9E3779B97F4A7C15^uint64(seq))))
		s, q, ops := genSequence(rng)
		log := runDifferential(t, s, q, ops, seq < 3)
		fmt.Fprintf(f, "===== sequence %d =====\n%s", seq, log)
		t.Logf("sequence %4d: S=%d Q=%d ops=%3d -> identical", seq, s, q, len(ops))
	}
}

// NowOrLast 供不变量检查取一个合法时间。
func (o Op) NowOrLast() int64 {
	if o.Now < 0 {
		return 0
	}
	if o.Now > 1_000_000_000_000 {
		return 1_000_000_000_000
	}
	return o.Now
}
