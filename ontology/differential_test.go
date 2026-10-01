package ontology

import (
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
)

func sameErr(a, b error) bool { return errName(a) == errName(b) }

// TestRandomDifferential2000 用 2000 组随机操作序列对照朴素参考模型，
// 每一步都比较返回值、编号分配、全部在册段的 Stats 与逐词项倒排表，
// 输入/输出/判定依据写入 fuzz_operations.log。
func TestRandomDifferential2000(t *testing.T) {
	logPath := filepath.Join(".", "fuzz_operations.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	t.Logf("fuzz log: %s", logPath)

	for seq := range 2000 {
		rng := rand.New(rand.NewPCG(uint64(seq)*2654435761+1, 0xBADC0FFEE))
		fs := newFuzzState(rng, seq, logFile)
		nOps := 40 + rng.IntN(41)

		for opIdx := range nOps {
			kind := rng.IntN(12)
			switch {
			case kind <= 3: // Register
				batch := fs.genBatch()
				idA, errA := fs.m.Register(batch)
				idB, errB := fs.orc.register(batch)
				in := fmt.Sprintf("Register(%v)", batch)
				out := fmt.Sprintf("impl=(id=%d,err=%s) naive=(id=%d,err=%s)",
					idA, errName(errA), idB, errName(errB))
				if idA != idB || !sameErr(errA, errB) {
					fs.recordOp(opIdx, in, out, "register result differs")
					t.Fatalf("seq=%d op=%d register mismatch", seq, opIdx)
				}
				if errA == nil {
					fs.allIDs = append(fs.allIDs, idA)
					fs.liveIDs = append(fs.liveIDs, idA)
					for _, d := range batch {
						fs.everKey[d.Key] = true
					}
				}
				fs.fullCheck(t, opIdx, in, out)

			case kind == 4: // Delete
				key := fs.pickKey()
				errA := fs.m.Delete(key)
				errB := fs.orc.delete(key)
				in := fmt.Sprintf("Delete(%q)", key)
				out := fmt.Sprintf("impl=(err=%s) naive=(err=%s)", errName(errA), errName(errB))
				if !sameErr(errA, errB) {
					fs.recordOp(opIdx, in, out, "delete result differs")
					t.Fatalf("seq=%d op=%d delete mismatch", seq, opIdx)
				}
				fs.fullCheck(t, opIdx, in, out)

			case kind <= 7: // BeginMerge
				ids := fs.mergeIDs()
				hA, errA := fs.m.BeginMerge(ids)
				hB, errB := fs.orc.beginMerge(ids)
				in := fmt.Sprintf("BeginMerge(%v)", ids)
				out := fmt.Sprintf("impl=(handle=%d,err=%s) naive=(handle=%d,err=%s)",
					hA, errName(errA), hB, errName(errB))
				if hA != hB || !sameErr(errA, errB) {
					fs.recordOp(opIdx, in, out, "begin merge result differs")
					t.Fatalf("seq=%d op=%d begin merge mismatch", seq, opIdx)
				}
				if errA == nil {
					fs.merges = append(fs.merges, &fuzzMerge{handle: hA})
				}
				fs.fullCheck(t, opIdx, in, out)

			case kind <= 9: // Commit
				handle := fs.pickHandle()
				idA, errA := fs.m.Commit(handle)
				idB, errB := fs.orc.commit(handle)
				in := fmt.Sprintf("Commit(%d)", handle)
				out := fmt.Sprintf("impl=(id=%d,err=%s) naive=(id=%d,err=%s)",
					idA, errName(errA), idB, errName(errB))
				if idA != idB || !sameErr(errA, errB) {
					fs.recordOp(opIdx, in, out, "commit result differs")
					t.Fatalf("seq=%d op=%d commit mismatch", seq, opIdx)
				}
				if errA == nil {
					fs.allIDs = append(fs.allIDs, idA)
					var inputs []int
					for _, mg := range fs.orc.merges {
						if mg.id == handle {
							inputs = mg.inputs
						}
					}
					removed := map[int]bool{}
					for _, id := range inputs {
						removed[id] = true
					}
					var next []int
					for _, id := range fs.liveIDs {
						if !removed[id] {
							next = append(next, id)
						}
					}
					next = append(next, idA)
					fs.liveIDs = next
					for _, mg := range fs.merges {
						if mg.handle == handle {
							mg.finished = true
						}
					}
				}
				fs.fullCheck(t, opIdx, in, out)

			case kind == 10: // Abort
				handle := fs.pickHandle()
				errA := fs.m.Abort(handle)
				errB := fs.orc.abort(handle)
				in := fmt.Sprintf("Abort(%d)", handle)
				out := fmt.Sprintf("impl=(err=%s) naive=(err=%s)", errName(errA), errName(errB))
				if !sameErr(errA, errB) {
					fs.recordOp(opIdx, in, out, "abort result differs")
					t.Fatalf("seq=%d op=%d abort mismatch", seq, opIdx)
				}
				if errA == nil {
					for _, mg := range fs.merges {
						if mg.handle == handle {
							mg.finished = true
						}
					}
				}
				fs.fullCheck(t, opIdx, in, out)

			default: // Stats + Postings 只读查询
				id := fs.pickSegmentID()
				term := fuzzTerms[rng.IntN(len(fuzzTerms))]
				stA, errSA := fs.m.Stats(id)
				_, errPA := fs.m.Postings(id, term)
				var stB Stats
				var errSB, errPB error
				if seg, ok := fs.orc.segments[id]; ok {
					stB = refStats(seg)
					_ = refPostings(seg, term)
				} else {
					errSB, errPB = ErrSegmentNotFound, ErrSegmentNotFound
				}
				in := fmt.Sprintf("Stats(%d)/Postings(%d,%q)", id, id, term)
				out := fmt.Sprintf("impl=(stats=%+v,statsErr=%s,postErr=%s) naive=(stats=%+v,statsErr=%s,postErr=%s)",
					stA, errName(errSA), errName(errPA), stB, errName(errSB), errName(errPB))
				if errName(errSA) != errName(errSB) || errName(errPA) != errName(errPB) {
					fs.recordOp(opIdx, in, out, "query error differs")
					t.Fatalf("seq=%d op=%d query mismatch", seq, opIdx)
				}
				if errSA == nil && stA != stB {
					fs.recordOp(opIdx, in, out, "stats value differs")
					t.Fatalf("seq=%d op=%d stats mismatch", seq, opIdx)
				}
				fs.fullCheck(t, opIdx, in, out)
			}
		}
	}
}

// TestDeterministicReplay 相同操作序列重放得到相同段编号、统计量与倒排表。
func TestDeterministicReplay(t *testing.T) {
	run := func(seed uint64) []string {
		rng := rand.New(rand.NewPCG(seed, seed+1))
		m := NewMerger()
		var log []string
		var live []int
		var pending []int
		for range 200 {
			switch rng.IntN(4) {
			case 0:
				key := fmt.Sprintf("k%d", rng.IntN(20))
				terms := []string{fmt.Sprintf("t%d", rng.IntN(5)), fmt.Sprintf("t%d", rng.IntN(5))}
				id, err := m.Register([]Doc{{Key: key, Terms: terms}})
				log = append(log, fmt.Sprintf("R %d %s", id, errName(err)))
				if err == nil {
					live = append(live, id)
				}
			case 1:
				if len(live) >= 2 {
					i, j := rng.IntN(len(live)), rng.IntN(len(live))
					h, err := m.BeginMerge([]int{live[i], live[j]})
					log = append(log, fmt.Sprintf("B %d %s", h, errName(err)))
					if err == nil {
						pending = append(pending, h)
					}
				}
			case 2:
				if len(pending) > 0 {
					h := pending[0]
					pending = pending[1:]
					id, err := m.Commit(h)
					log = append(log, fmt.Sprintf("C %d %s", id, errName(err)))
					if err == nil {
						live = append(live, id)
					}
				}
			case 3:
				if err := m.Delete(fmt.Sprintf("k%d", rng.IntN(20))); err != nil {
					log = append(log, "D err")
				} else {
					log = append(log, "D ok")
				}
			}
		}
		for _, id := range live {
			st, _ := m.Stats(id)
			log = append(log, fmt.Sprintf("S %d %+v", id, st))
		}
		return log
	}
	first := run(42)
	second := run(42)
	if len(first) != len(second) {
		t.Fatalf("replay length differs: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("replay differs at step %d:\n%s\n%s", i, first[i], second[i])
		}
	}
}
