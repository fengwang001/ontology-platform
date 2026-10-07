package recovery

import (
	"bytes"
	"fmt"
	"math/rand"
	"testing"
)

type genCase struct {
	base            Version
	snap            Snapshot
	brokenSnap      []byte
	actions         []Action
	brokenLog       []byte
	corruptedAction []bool
}

// generateCase 随机构造快照损坏模式、动作序列与动作损坏模式。
// t 允许为 nil（benchmark 中复用），出错时直接 panic。
func generateCase(t *testing.T, rng *rand.Rand, nObjects, nActions int) genCase {
	flip := func(b []byte, off int) []byte {
		if t != nil {
			return flipByte(t, b, off)
		}
		out := append([]byte(nil), b...)
		out[off] ^= 0xFF
		return out
	}
	find := func(haystack []byte, needle string) int {
		off := bytes.Index(haystack, []byte(needle))
		if off < 0 {
			panic("needle not found: " + needle)
		}
		return off
	}

	base := Version{Epoch: rng.Intn(3), Seq: int64(rng.Intn(10))}
	objs := make([]ObjectSnapshot, 0, nObjects)
	ids := make([]ObjectID, 0, nObjects)
	for i := 0; i < nObjects; i++ {
		id := ObjectID(fmt.Sprintf("o%02d", i))
		ids = append(ids, id)
		objs = append(objs, ObjectSnapshot{
			ID: id, Exists: true, State: State(fmt.Sprintf("%s-snap", id)),
		})
	}
	snap := Snapshot{BaseVersion: base, Objects: objs}
	snapRaw, err := EncodeSnapshot(snap)
	if err != nil {
		panic(err)
	}
	for i := 0; i < nObjects; i++ {
		if rng.Intn(3) == 0 { // 约 1/3 的快照记录损坏
			hdr := fmt.Sprintf(`{"id":%q}`, ids[i])
			off := find(snapRaw, hdr)
			snapRaw = flip(snapRaw, off+len(hdr)+8)
		}
	}

	actions := make([]Action, nActions)
	corrupted := make([]bool, nActions)
	for i := 0; i < nActions; i++ {
		setSize := 1 + rng.Intn(3)
		eff := map[ObjectID]ObjectEffect{}
		for j := 0; j < setSize; j++ {
			id := ids[rng.Intn(nObjects)]
			e := ObjectEffect{Change: State(fmt.Sprintf("%s@%d", id, i))}
			if rng.Intn(2) == 0 {
				s := State(fmt.Sprintf("%s-start@%d", id, i))
				e.Start = &s
			}
			eff[id] = e
		}
		actions[i] = Action{
			Seq:     i + 1,
			Version: Version{Epoch: base.Epoch, Seq: base.Seq + int64(i) + 1},
			Effects: eff,
		}
	}
	logRaw, err := EncodeLog(base, actions)
	if err != nil {
		panic(err)
	}
	for i := 0; i < nActions; i++ {
		if rng.Intn(4) == 0 { // 约 1/4 的动作帧损坏
			needle := fmt.Sprintf(`"seq":%d,`, i+1)
			off := find(logRaw, needle)
			logRaw = flip(logRaw, off+len(needle)+2)
			corrupted[i] = true
		}
	}
	return genCase{
		base: base, snap: snap, brokenSnap: snapRaw,
		actions: actions, brokenLog: logRaw, corruptedAction: corrupted,
	}
}

// TestRandomDifferentialVsNaive 与独立朴素重放模型对拍随机构造的
// 损坏模式与动作序列：状态、来源分类、锚点序号、判定序列必须逐项一致。
func TestRandomDifferentialVsNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261007))
	for iter := 0; iter < 300; iter++ {
		gc := generateCase(t, rng, 1+rng.Intn(6), rng.Intn(12))

		decodedSnap, corruptObjs, err := DecodeSnapshot(gc.brokenSnap)
		if err != nil {
			t.Fatalf("iter %d: snapshot hard error: %v", iter, err)
		}
		logStart, decodedActions, corruptErrs, err := DecodeLog(gc.brokenLog)
		if err != nil {
			t.Fatalf("iter %d: log hard error: %v", iter, err)
		}
		naive, err := NewNaiveReplay(decodedSnap, corruptObjs, logStart,
			decodedActions, corruptErrs)
		if err != nil {
			t.Fatalf("iter %d: naive: %v", iter, err)
		}

		c, err := NewCoordinator(gc.brokenSnap, gc.brokenLog, NopLogger{})
		if err != nil {
			t.Fatalf("iter %d: coordinator: %v", iter, err)
		}
		rep, err := c.RepairAll()
		if err != nil {
			t.Fatalf("iter %d: repair: %v", iter, err)
		}

		if len(rep.Judgments) != len(naive.Judgments()) {
			t.Fatalf("iter %d: judgment count %d vs naive %d",
				iter, len(rep.Judgments), len(naive.Judgments()))
		}
		for i, j := range rep.Judgments {
			nj := naive.Judgments()[i]
			if j.Outcome != nj.Outcome || j.Seq != nj.Seq {
				t.Fatalf("iter %d judgment %d: %+v vs naive %+v", iter, i, j, nj)
			}
		}
		for i := range gc.snap.Objects {
			id := gc.snap.Objects[i].ID
			got := rep.Objects[id]
			want, ok := naive.Report(id)
			if !ok {
				t.Fatalf("iter %d: %s missing in naive coverage", iter, id)
			}
			if got.Exists != want.Exists || got.State != want.State ||
				got.Source != want.Source || got.AnchorSeq != want.AnchorSeq ||
				got.SnapshotUnreadable != want.SnapshotUnreadable {
				t.Fatalf("iter %d object %s:\n coord %+v\n naive %+v",
					iter, id, got, want)
			}
		}
	}
}

func buildScaleCoordinator(t *testing.T, n int) *Coordinator {
	base := Version{Epoch: 1, Seq: 0}
	snap := Snapshot{BaseVersion: base, Objects: []ObjectSnapshot{
		{ID: "target", Exists: true, State: "T0"},
	}}
	actions := make([]Action, n)
	for i := range actions {
		other := ObjectID(fmt.Sprintf("noise%05d", i))
		s := State("s")
		actions[i] = Action{
			Seq:     i + 1,
			Version: Version{Epoch: 1, Seq: int64(i + 1)},
			Effects: map[ObjectID]ObjectEffect{
				other: {Start: &s, Change: State(fmt.Sprintf("v%d", i))},
			},
		}
	}
	c, err := NewCoordinator(mustEncodeSnapshot(t, snap),
		mustEncodeLog(t, base, actions), NopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestLookupScaleIndependent 给出“定位来源分类不随日志长度增长”的
// 可验证证据：10 倍日志长度下单次 Lookup 的 ns/op 之比不得超过常数 3，
// 且每次查询零额外分配（只读物化投影 map）。
func TestLookupScaleIndependent(t *testing.T) {
	bench := func(c *Coordinator) testing.BenchmarkResult {
		return testing.Benchmark(func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := c.Lookup("target"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
	small := bench(buildScaleCoordinator(t, 2000))
	large := bench(buildScaleCoordinator(t, 20000))
	sp, lp := small.NsPerOp(), large.NsPerOp()
	if sp <= 0 {
		sp = 1
	}
	t.Logf("Lookup ns/op: 2k=%d 20k=%d ratio=%.2f; allocs/op 2k=%d 20k=%d",
		sp, lp, float64(lp)/float64(sp),
		small.AllocsPerOp(), large.AllocsPerOp())
	if float64(lp)/float64(sp) > 3.0 {
		t.Fatalf("Lookup appears to scale with log length: ratio=%.2f", float64(lp)/float64(sp))
	}
	if large.AllocsPerOp() != 0 {
		t.Fatalf("Lookup must be allocation-free, got %d allocs/op", large.AllocsPerOp())
	}
}
