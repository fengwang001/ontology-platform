package flexray

import (
	"fmt"
	"math/rand"
	"testing"
)

// replayOne 用固定 rng 构造并执行一段操作序列，收集可观测输出快照。
func replayOne(t *testing.T, seed int64) string {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	ns, nm := 3, 8
	lt := rng.Intn(nm + 1)
	a, err := New(ns, nm, lt)
	if err != nil {
		t.Fatal(err)
	}
	total := ns + nm
	out := fmt.Sprintf("New(%d,%d,%d)\n", ns, nm, lt)
	for step := 0; step < 100; step++ {
		switch rng.Intn(4) {
		case 0:
			id := 1 + rng.Intn(total)
			rep := repChoices[rng.Intn(len(repChoices))]
			base := rng.Intn(rep)
			err := a.Assign(id, base, rep)
			out += fmt.Sprintf("Assign(%d,%d,%d)->%v\n", id, base, rep, errCodeOf(err))
		case 1:
			id := 1 + rng.Intn(total)
			L := 1 + rng.Intn(nm)
			tag := fmt.Sprintf("t%d", step)
			err := a.Post(id, L, tag)
			out += fmt.Sprintf("Post(%d,%d,%s)->%v\n", id, L, tag, errCodeOf(err))
		case 2:
			r := a.Cycle()
			out += fmt.Sprintf("Cycle->{c:%d u:%d e:%d sent:%v}\n", r.C, r.Unused, r.EmptyFrame, r.Sent)
		default:
			id := 1 + rng.Intn(total)
			tags, err := a.Pending(id)
			out += fmt.Sprintf("Pending(%d)->%v(%v)\n", id, tags, errCodeOf(err))
		}
	}
	out += fmt.Sprintf("final stats=%+v c=%d\n", a.Stats(), a.C())
	return out
}

// TestDeterministicReplay 相同操作序列重放得到完全相同的清单与计数。
func TestDeterministicReplay(t *testing.T) {
	for seed := int64(1); seed <= 20; seed++ {
		first := replayOne(t, seed)
		second := replayOne(t, seed)
		if first != second {
			t.Fatalf("seed %d replay differs", seed)
		}
	}
}
