package mirror_test

import (
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"

	"ontology/mirror"
	"ontology/naive"
)

// opOutcome 是一次操作在某一实现上的可比较结果。
type opOutcome struct {
	kind int // 错误类别；-1 表示成功
	out  string
}

func mirrorKind(err error) int {
	if err == nil {
		return -1
	}
	k, ok := mirror.KindOf(err)
	if !ok {
		return -2
	}
	return int(k)
}

func naiveKind(err error) int {
	if err == nil {
		return -1
	}
	if e, ok := err.(*naive.Err); ok {
		return int(e.Kind)
	}
	return -2
}

// runMirror 在正式实现上执行一条随机操作并返回输入描述与结果。
func runMirror(v *mirror.Volume, r *rand.Rand, numBlocks, numMembers int) (string, opOutcome) {
	return runOp(r, numBlocks, numMembers, v.Snapshot(), mirrorKind,
		v.Write, v.Read, v.ReportFault, v.Rejoin, v.ResyncAdvance)
}

func runNaive(v *naive.Vol, r *rand.Rand, numBlocks, numMembers int, snap mirror.Snapshot) (string, opOutcome) {
	return runOp(r, numBlocks, numMembers, snap, naiveKind,
		v.Write, v.Read, v.ReportFault, v.Rejoin, v.ResyncAdvance)
}

// runOp 用同一份随机数流生成操作，保证两个实现收到完全相同的输入。
// 由于随机操作依赖卷状态（如故障世代标签），调用方须保证传入的快照一致。
func runOp(
	r *rand.Rand, numBlocks, numMembers int, snap mirror.Snapshot,
	kindOf func(error) int,
	write func(int, string, []int) error,
	read func(int) (string, int, error),
	reportFault func(int) error,
	rejoin func(int, uint64) error,
	advance func(int, int) ([]int, error),
) (string, opOutcome) {
	switch r.Intn(100) {
	case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19,
		20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39:
		block := r.Intn(numBlocks + 1) // 偶尔越界
		value := fmt.Sprintf("v%d", r.Intn(1000))
		var failed []int
		if r.Intn(10) == 0 {
			for i := 0; i < numMembers; i++ { // 偶尔注入全员失败
				failed = append(failed, i)
			}
		} else {
			for i := 0; i < numMembers; i++ {
				if r.Intn(4) == 0 {
					failed = append(failed, i)
				}
			}
		}
		if r.Intn(30) == 0 {
			failed = append(failed, numMembers+r.Intn(2)) // 偶尔含不存在的成员
		}
		err := write(block, value, failed)
		return fmt.Sprintf("Write(block=%d, val=%s, failed=%v)", block, value, failed),
			opOutcome{kind: kindOf(err), out: ""}
	case 40, 41, 42, 43, 44, 45, 46, 47, 48, 49, 50, 51, 52, 53, 54:
		block := r.Intn(numBlocks + 1)
		val, from, err := read(block)
		return fmt.Sprintf("Read(block=%d)", block),
			opOutcome{kind: kindOf(err), out: fmt.Sprintf("val=%q from=%d", val, from)}
	case 55, 56, 57, 58, 59, 60, 61, 62, 63, 64:
		id := r.Intn(numMembers+1) - 1 // 偶尔为 -1 或 numMembers
		err := reportFault(id)
		return fmt.Sprintf("ReportFault(id=%d)", id), opOutcome{kind: kindOf(err)}
	case 65, 66, 67, 68, 69, 70, 71, 72, 73, 74, 75, 76, 77, 78, 79:
		id := r.Intn(numMembers)
		gen := snap.Generation
		fg := snap.Members[id].FaultGen
		choices := []uint64{gen + 1, fg, fg + 1, gen}
		if fg > 0 {
			choices = append(choices, fg-1)
		}
		diskGen := choices[r.Intn(len(choices))]
		err := rejoin(id, diskGen)
		return fmt.Sprintf("Rejoin(id=%d, diskGen=%d)", id, diskGen), opOutcome{kind: kindOf(err)}
	default:
		id := r.Intn(numMembers)
		maxBlocks := 1 + r.Intn(4)
		copied, err := advance(id, maxBlocks)
		return fmt.Sprintf("ResyncAdvance(id=%d, max=%d)", id, maxBlocks),
			opOutcome{kind: kindOf(err), out: fmt.Sprintf("copied=%v", copied)}
	}
}

// TestDifferentialRandom 用同一随机操作序列同时驱动正式实现与朴素模型，
// 逐条比较错误类别、输出与完整状态摘要，并打印每条操作的输入、输出与判定依据。
func TestDifferentialRandom(t *testing.T) {
	for seed := int64(0); seed < 30; seed++ {
		r := rand.New(rand.NewSource(seed))
		numMembers := 2 + r.Intn(3)
		numBlocks := 1 + r.Intn(12)
		limit := r.Intn(6)
		mv, err := mirror.NewVolume(numMembers, numBlocks, limit)
		if err != nil {
			t.Fatalf("seed=%d 建卷失败: %v", seed, err)
		}
		nv, err := naive.New(numMembers, numBlocks, limit)
		if err != nil {
			t.Fatalf("seed=%d 建模失败: %v", seed, err)
		}
		t.Logf("seed=%d 配置: 成员=%d 块=%d 脏区上限=%d", seed, numMembers, numBlocks, limit)
		for step := 0; step < 300; step++ {
			snap := mv.Snapshot()
			// 用同一随机种子副本为两个实现生成同一条操作。
			opSeed := r.Int63()
			rMirror := rand.New(rand.NewSource(opSeed))
			rNaive := rand.New(rand.NewSource(opSeed))
			desc, gotM := runMirror(mv, rMirror, numBlocks, numMembers)
			_, gotN := runNaive(nv, rNaive, numBlocks, numMembers, snap)
			verdict := "一致"
			if gotM != gotN {
				verdict = fmt.Sprintf("不一致: mirror=%+v naive=%+v", gotM, gotN)
			}
			dM, dN := mv.Digest(), nv.Digest()
			if dM != dN {
				verdict += "；状态摘要不一致"
			}
			t.Logf("seed=%d step=%03d 输入=%s | mirror=%+v naive=%+v | 判定: %s",
				seed, step, desc, gotM, gotN, verdict)
			if gotM != gotN {
				t.Fatalf("seed=%d step=%d 输出不一致: %s", seed, step, verdict)
			}
			if dM != dN {
				t.Fatalf("seed=%d step=%d 状态不一致\nmirror: %s\nnaive : %s", seed, step, dM, dN)
			}
		}
	}
}

// TestConcurrentSmoke 并发驱动全部操作，依赖互斥锁保证结果等价于某个串行顺序；
// 配合 -race 运行以检测数据竞争，结束后校验状态摘要自洽。
func TestConcurrentSmoke(t *testing.T) {
	v, err := mirror.NewVolume(4, 32, 4)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			for i := 0; i < 300; i++ {
				runMirror(v, r, 32, 4)
			}
		}(int64(g) * 977)
	}
	wg.Wait()
	snap := v.Snapshot()
	if snap.Generation < 1 {
		t.Fatalf("世代不应小于 1，实际 %d", snap.Generation)
	}
	online := 0
	for _, ms := range snap.Members {
		if ms.State == mirror.Online {
			online++
		}
		if ms.State == mirror.Online && ms.Pending != nil {
			t.Fatalf("在线成员不应有待同步块: %+v", ms)
		}
	}
	if online == 0 {
		for _, ms := range snap.Members {
			if ms.State != mirror.Faulted {
				t.Fatalf("没有在线成员时其余成员应全部故障，实际 %s", ms.State)
			}
		}
	}
	if !strings.Contains(v.Digest(), "gen=") {
		t.Fatalf("摘要应包含世代")
	}
}
