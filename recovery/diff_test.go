package recovery

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

// genCase 随机构造损坏模式与动作序列。
//
// 随机维度：
//   - 对象数量；每个对象快照状态：完好 / 损坏 / 不存在；
//   - 动作数量、每动作涉及的对象子集（含单对象、跨对象边界）；
//   - 每个效果：仅增量 vs 自带完整起点；
//   - 每条动作记录：完好 vs 在随机字段上损坏（整条应失配）。
type genCase struct {
	snap    Snapshot
	log     Log
	actions []Action // 剔除损坏后的期望完整序列（供朴素模型）
}

func generate(rng *rand.Rand) genCase {
	nObj := 2 + rng.Intn(5)
	objs := make([]string, nObj)
	for i := range objs {
		objs[i] = fmt.Sprintf("o%d", i)
	}

	snap := Snapshot{Version: "v1"}
	for _, obj := range objs {
		switch rng.Intn(3) {
		case 0: // 不存在
		case 1: // 完好
			st := fmt.Sprintf("snap-%s", obj)
			snap.Records = append(snap.Records, goodSnapRecord(obj, st))
		case 2: // 损坏
			snap.Records = append(snap.Records, SnapshotRecord{
				ObjectID: obj,
				State:    fmt.Sprintf("snap-%s", obj),
				Checksum: "CORRUPT",
			})
		}
	}

	nAct := rng.Intn(12)
	gc := genCase{snap: snap, log: Log{Base: "v1"}}
	for i := 0; i < nAct; i++ {
		k := 1 + rng.Intn(nObj)
		rng.Shuffle(nObj, func(a, b int) { objs[a], objs[b] = objs[b], objs[a] })
		effects := map[ObjectID]Effect{}
		for j := 0; j < k; j++ {
			obj := objs[j]
			if rng.Intn(2) == 0 {
				effects[obj] = delta(fmt.Sprintf("a%d-d", i))
			} else {
				effects[obj] = anchored(fmt.Sprintf("a%d-start-%s", i, obj), fmt.Sprintf("a%d-c", i))
			}
		}
		id := fmt.Sprintf("act%d", i)
		rec := goodAction(id, "v1", effects)
		if rng.Intn(3) == 0 {
			// 在随机字段上制造损坏：改动某个对象的一个字段或直接破坏校验值。
			switch rng.Intn(3) {
			case 0:
				rec.Checksum = "BROKEN"
			case 1:
				for obj := range rec.Effects {
					ef := rec.Effects[obj]
					ef.Change = ef.Change + "-TAMPER"
					rec.Effects[obj] = ef
					break
				}
			case 2:
				for obj := range rec.Effects {
					ef := rec.Effects[obj]
					ef.HasStart = !ef.HasStart
					rec.Effects[obj] = ef
					break
				}
			}
			gc.log.Records = append(gc.log.Records, rec)
		} else {
			gc.log.Records = append(gc.log.Records, rec)
			gc.actions = append(gc.actions, Action{ActionID: id, Base: "v1", Effects: effects})
		}
	}
	return gc
}

// TestRandomDifferentialAgainstNaive 与独立朴素重放模型对照。
func TestRandomDifferentialAgainstNaive(t *testing.T) {
	const iterations = 500
	for seed := int64(0); seed < iterations; seed++ {
		rng := rand.New(rand.NewSource(seed))
		gc := generate(rng)

		// 协调器路径。
		c, err := NewCoordinator(gc.snap, gc.log)
		if err != nil {
			t.Fatalf("seed %d: unexpected error %v", seed, err)
		}
		got, err := c.Recover(nil)
		if err != nil {
			t.Fatalf("seed %d: recover %v", seed, err)
		}

		// 朴素路径：喂入“期望完整动作”，同时朴素模型内部自行重算快照校验，
		// 因此直接喂原始快照即可；但朴素模型无法看到被损坏的动作，
		// gc.actions 已是剔除损坏后的序列。
		want := naiveReplay(gc.snap, gc.actions)

		if len(got.Objects) != len(want) {
			t.Fatalf("seed %d: coverage got=%d want=%d", seed, len(got.Objects), len(want))
		}
		for obj, w := range want {
			g, ok := got.Objects[obj]
			if !ok {
				t.Fatalf("seed %d: object %s missing", seed, obj)
			}
			if g != w {
				t.Fatalf("seed %d: object %s mismatch\ngot  = %+v\nwant = %+v", seed, obj, g, w)
			}
		}

		// 损坏清单也要一致。
		lv := verifyActions(&gc.log)
		if len(got.CorruptActions) != len(lv.corrupt) {
			t.Fatalf("seed %d: corrupt action list %v vs %v", seed, got.CorruptActions, lv.corrupt)
		}

		// 对第一个种子打印一次完整判定日志，示范输入/输出/依据。
		if seed == 0 {
			dumpDecisionLog(t, c, fmt.Sprintf("random-seed-%d", seed))
		}
	}
}

// TestRandomIncrementalMatchesBatch 随机追加路径必须与一次性批处理一致。
func TestRandomIncrementalMatchesBatch(t *testing.T) {
	const iterations = 200
	for seed := int64(0); seed < iterations; seed++ {
		rng := rand.New(rand.NewSource(10000 + seed))
		gc := generate(rng)

		batch, err := Evaluate(gc.snap, gc.log, nil)
		if err != nil {
			t.Fatalf("seed %d: batch %v", seed, err)
		}

		c, err := NewCoordinator(gc.snap, Log{Base: gc.log.Base})
		if err != nil {
			t.Fatalf("seed %d: new %v", seed, err)
		}
		for _, rec := range gc.log.Records {
			if err := c.AppendAction(rec); err != nil {
				// 损坏动作追加被拒：与批处理把它整条丢弃等价。
				if !errors.Is(err, ErrActionCorrupt) {
					t.Fatalf("seed %d: unexpected append err %v", seed, err)
				}
			}
		}
		inc, err := c.Recover(nil)
		if err != nil {
			t.Fatalf("seed %d: recover %v", seed, err)
		}

		if len(batch.Objects) != len(inc.Objects) {
			t.Fatalf("seed %d: coverage %d vs %d", seed, len(batch.Objects), len(inc.Objects))
		}
		for obj, b := range batch.Objects {
			if inc.Objects[obj] != b {
				t.Fatalf("seed %d obj %s: batch=%+v incremental=%+v",
					seed, obj, b, inc.Objects[obj])
			}
		}
	}
}
