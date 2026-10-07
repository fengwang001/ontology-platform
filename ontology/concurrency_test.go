package ontology

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
)

// 场景 5：同一输入列表以不同内部并发度执行，
// 逐条钩子判定与整批结果完全一致。
func TestSameResultAcrossInternalConcurrency(t *testing.T) {
	fail := map[string]string{"b": "bad_b", "e": "bad_e"}
	var base []Record
	for i := 0; i < 40; i++ {
		base = append(base, rec(fmt.Sprintf("k%02d", i), int64(i)))
	}
	base = append(base, rec("b", 1), rec("e", 2))

	snapshot := func() []Record {
		out := make([]Record, len(base))
		copy(out, base)
		return out
	}

	var reference *BatchResult
	workerCounts := []int{1, 2, 4, 8, 16}
	for _, w := range workerCounts {
		var seen []float64
		reg := testRegistry(countingPre(&seen, fail), func(ctx *HookContext, view HookView) *HookError {
			return nil // 后置钩子总通过
		})
		st := NewStore(reg)
		rng := rand.New(rand.NewSource(int64(w * 1009)))
		im := NewImporter(reg, w).WithJitter(func(index int) {
			if rng.Intn(3) == 0 {
				spin(rng.Intn(200))
			}
		})
		res := im.Import(st, Batch{
			Type: testTypeName, Semantic: SemBestEffort, Records: snapshot(),
		})
		dump(t, fmt.Sprintf("concurrency-workers=%d", w),
			fmt.Sprintf("%d records, forced fails b,e", len(base)),
			resultsSummary(res.Records), "identical status/error vector across all w",
			"无论内部并发度与随机调度如何，按列表顺序解析的结果不变")
		if reference == nil {
			reference = res
		} else if !equalResults(reference.Records, res.Records) {
			t.Fatalf("worker=%d result vector differs", w)
		}
	}

	// 全有全无路径也覆盖一次：不同并发度下均整体失败且状态为空。
	for _, w := range workerCounts {
		var seen []float64
		reg := testRegistry(countingPre(&seen, fail), nil)
		st := NewStore(reg)
		rng := rand.New(rand.NewSource(int64(w)))
		res := NewImporter(reg, w).WithJitter(func(index int) {
			if rng.Intn(4) == 0 {
				spin(rng.Intn(150))
			}
		}).Import(st, Batch{
			Type: testTypeName, Semantic: SemAllOrNothing, Records: snapshot(),
		})
		if res.Committed || len(st.CurrentInstances(testTypeName)) != 0 {
			t.Fatalf("worker=%d all-or-nothing failure not atomic", w)
		}
	}
}

func spin(n int) {
	x := 0
	for i := 0; i < n; i++ {
		x += i
	}
	_ = x
}

func equalResults(a, b []RecordResult) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Status != b[i].Status {
			return false
		}
		if (a[i].Err == nil) != (b[i].Err == nil) {
			return false
		}
		if a[i].Err != nil &&
			(a[i].Err.Kind != b[i].Err.Kind || a[i].Err.Code != b[i].Err.Code) {
			return false
		}
	}
	return true
}

// 场景 6：并发发起的多个独立批次，最终状态等价于某个全局串行顺序。
// 每个批次对独立主键空间做加法（sum 断言），无论如何交错，
// 最终每个主键集合与 sum 都应等于全部批次效果的并集。
func TestConcurrentBatchesAreSerializable(t *testing.T) {
	reg := testRegistry(
		func(ctx *HookContext, r Record, view HookView) *HookError { return nil }, nil)
	st := NewStore(reg)

	const batchesN, perBatch = 12, 50
	var wg sync.WaitGroup
	for g := 0; g < batchesN; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			var recs []Record
			for i := 0; i < perBatch; i++ {
				pk := fmt.Sprintf("g%02d-k%02d", g, i)
				recs = append(recs, rec(pk, int64(g*1000+i)))
			}
			res := NewImporter(reg, 1+g%8).Import(st, Batch{
				Type: testTypeName, Semantic: SemBestEffort, Records: recs,
			})
			if !res.Committed {
				t.Errorf("batch %d failed unexpectedly: %+v", g, res.FirstError())
			}
		}(g)
	}
	wg.Wait()

	inst := st.CurrentInstances(testTypeName)
	if len(inst) != batchesN*perBatch {
		t.Fatalf("serializable union size=%d want %d", len(inst), batchesN*perBatch)
	}
	// 版本号应恰好等于成功提交批次数（每个批次一次原子提交）。
	if v := st.CurrentVersion(testTypeName); v != int64(batchesN) {
		t.Fatalf("commit version=%d want %d", v, batchesN)
	}
	// 抽样断言并发交错没有丢失/串改写入。
	if got := inst["g07-k03"]["v"].(int64); got != 7003 {
		t.Fatalf("concurrent write corrupted: %d", got)
	}
	dump(t, "concurrent-batches",
		fmt.Sprintf("%d batches x %d records, disjoint keys", batchesN, perBatch),
		len(inst), batchesN*perBatch,
		"最终实例数等于所有批次效果并集，版本号等于批次数 => 等价于某种全局串行顺序")
}

// 场景 7：重放同一批次输入，得到完全相同的逐条结果与最终状态。
func TestReplayDeterminism(t *testing.T) {
	fail := map[string]string{"x": "bad_x"}
	records := []Record{
		rec("a", 1), rec("x", 2), rec("c", 3), rec("d", 4),
	}

	run := func(seed int64) (*BatchResult, []string) {
		var seen []float64
		reg := testRegistry(countingPre(&seen, fail), nil)
		st := NewStore(reg)
		rng := rand.New(rand.NewSource(seed))
		res := NewImporter(reg, 4).WithJitter(func(index int) {
			spin(rng.Intn(300))
		}).Import(st, Batch{
			Type: testTypeName, Semantic: SemBestEffort, Records: records,
		})
		keys := make([]string, 0)
		for k := range st.CurrentInstances(testTypeName) {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return res, keys
	}

	r1, k1 := run(1)
	for seed := int64(2); seed <= 5; seed++ {
		r2, k2 := run(seed)
		if !reflect.DeepEqual(resultsSummary(r1.Records), resultsSummary(r2.Records)) ||
			!reflect.DeepEqual(k1, k2) {
			t.Fatalf("replay differs under seed %d", seed)
		}
	}
	dump(t, "replay-determinism", join(recsToStrings(records)),
		resultsSummary(r1.Records), "identical across seeds 1..5",
		"重放同一输入列表，逐条结果与最终状态完全相同")
}
