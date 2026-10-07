package ontology

import (
	"math/rand"
	"strconv"
	"sync"
	"testing"
)

// 并发复核一致性：多个复核者在同一线性化快照上必须得到完全相同的结论。
func TestConcurrentVerifyConsistency(t *testing.T) {
	p, _ := newLoggedPlatform()
	p.DeclareIndex("T", "age")
	for i := 0; i < 50; i++ {
		p.Put("T", ObjectID("o"+strconv.Itoa(i)), "age", Value(strconv.Itoa(i%5)))
	}
	h, _ := p.StartRebuild("T", "age")
	for i := 0; i < 50; i++ {
		p.Put("T", ObjectID("n"+strconv.Itoa(i)), "age", Value(strconv.Itoa(i%7)))
	}
	h.Complete()

	const n = 16
	reps := make([]*VerificationReport, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			reps[i] = p.Verify("T", "age")
		}(i)
	}
	wg.Wait()
	base := reps[0]
	if !base.Consistent {
		t.Fatalf("基线复核本应一致: %+v", base.Mismatches)
	}
	for i := 1; i < n; i++ {
		if reps[i].SnapshotSeq != base.SnapshotSeq {
			t.Fatalf("并发复核应落在同一序号快照: %d vs %d", reps[i].SnapshotSeq, base.SnapshotSeq)
		}
		if !reportsEqual(reps[i], base) {
			t.Fatalf("并发复核结论不一致: %+v vs %+v", reps[i].Mismatches, base.Mismatches)
		}
	}
}

// 随机写入/重建交错序列，与独立朴素模型对照，验证基线+增量等价于全局串行顺序。
func TestRandomInterleavingAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	p, buf := newLoggedPlatform()
	p.DeclareIndex("T", "age")

	var handle *RebuildHandle
	rebuilding := false
	for step := 0; step < 4000; step++ {
		switch rng.Intn(10) {
		case 0, 1, 2, 3, 4, 5, 6:
			obj := ObjectID("o" + strconv.Itoa(rng.Intn(12)))
			if rng.Intn(8) == 0 {
				p.DeleteAttr("T", obj, "age")
			} else {
				p.Put("T", obj, "age", Value("v"+strconv.Itoa(rng.Intn(6))))
			}
		case 7:
			if !rebuilding {
				h, err := p.StartRebuild("T", "age")
				if err == nil {
					handle, rebuilding = h, true
				}
			}
		case 8:
			if rebuilding {
				if rng.Intn(5) == 0 {
					handle.Fail() // 注入失败：部分结果必须隔离
				} else {
					handle.Complete()
				}
				handle, rebuilding = nil, false
			}
		case 9:
			// 重建窗口外对可用索引做只读对照
			if !rebuilding {
				if st, ok := p.IndexStatusFor("T", "age"); ok && st == StatusAvailable {
					if d := p.DiffAgainstNaive("T", "age"); len(d) != 0 {
						t.Fatalf("step=%d 与朴素模型分歧: %v", step, d)
					}
				}
			}
		}
	}
	if rebuilding {
		handle.Complete()
	}
	// 最后一轮可能以失败收尾：重新成功重建后再对照。
	if st, _ := p.IndexStatusFor("T", "age"); st != StatusAvailable {
		h, _ := p.StartRebuild("T", "age")
		h.Complete()
	}
	if d := p.DiffAgainstNaive("T", "age"); len(d) != 0 {
		t.Fatalf("最终索引与朴素模型不一致: %v", d)
	}
	if rep := p.Verify("T", "age"); !rep.Consistent {
		t.Fatalf("最终复核应一致: %+v", rep.Mismatches)
	}
	if buf.Len() == 0 || !contains(buf.String(), `"basis"`) {
		t.Fatal("判定日志必须打印每次判定的输入、输出与依据")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
