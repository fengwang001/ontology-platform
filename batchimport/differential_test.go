package batchimport

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// TestRandomDifferentialAgainstNaive 在大量随机批量导入序列下，
// 将生产实现的逐条结果与最终状态，与独立朴素模型严格逐条对照。
// 每个用例打印输入、实际输出与判定依据（-v 时可见）。
func TestRandomDifferentialAgainstNaive(t *testing.T) {
	spec := hookSpec{sumPre: true, uniquePre: true, capPost: true, postCap: 64}
	const iterations = 400

	for iter := 0; iter < iterations; iter++ {
		seed := int64(1000 + iter)
		rng := rand.New(rand.NewSource(seed))

		// 两个相同初始状态的注册表（生产）与朴素模型种子。
		seedReg := NewRegistry()
		registerTestTypes(seedReg, spec)
		seedRecords := randomBatch(rng, 4, "seed")
		seedReg.Batch(seedRecords, BestEffort, 1)

		records := randomBatch(rng, 1+rng.Intn(12), "b")
		semantics := AllOrNothing
		if rng.Intn(2) == 1 {
			semantics = BestEffort
		}
		concurrency := []int{1, 1, 2, 8}[rng.Intn(4)]

		// 生产实现。
		prod := NewRegistry()
		registerTestTypes(prod, spec)
		cloneInto(seedReg, prod)
		rep := prod.Batch(records, semantics, concurrency)

		// 朴素模型从同一种子状态出发。
		naive := newNaiveModel(seedReg, spec)
		nrep := naive.run(records, semantics)

		// 朴素模型最终状态落到第二个生产注册表，以便用同一快照口径比较。
		naiveReg := NewRegistry()
		registerTestTypes(naiveReg, spec)
		cloneInto(seedReg, naiveReg)
		applyNaiveState(naiveReg, naive.data)

		fmt.Printf("---- iter=%d seed=%d semantics=%v concurrency=%d ----\nINPUT:\n%sACTUAL:\n%s",
			iter, seed, semantics, concurrency, formatRecords(records), formatReport(rep))

		if rep.Committed != nrep.committed {
			t.Fatalf("iter=%d 依据: 提交标记不一致 prod=%v naive=%v", iter, rep.Committed, nrep.committed)
		}
		if (rep.Err == nil) != (nrep.errKind == 0) {
			t.Fatalf("iter=%d 依据: 批次级错误有无不一致 prod=%v naiveKind=%d\nINPUT:\n%s%s",
				iter, rep.Err, nrep.errKind, formatRecords(records), formatReport(rep))
		}
		if rep.Err != nil {
			e, _ := AsImportError(rep.Err)
			if e.Kind != nrep.errKind || e.Index != nrep.errIndex {
				t.Fatalf("iter=%d 依据: 批次级错误 类别/下标 不一致 prod=(%d,%d) naive=(%d,%d)",
					iter, e.Kind, e.Index, nrep.errKind, nrep.errIndex)
			}
		}
		for i := range records {
			p, n := rep.Records[i], nrep.records[i]
			if p.OK != n.OK {
				t.Fatalf("iter=%d record[%d] 依据: 成功标志不一致 prod=%v naive=%v\nINPUT:\n%s%s",
					iter, i, p.OK, n.OK, formatRecords(records), formatReport(rep))
			}
			if !p.OK && n.Kind != 0 {
				if kindOfReason(p.Reason) != n.Kind {
					t.Fatalf("iter=%d record[%d] 依据: 错误类别不一致 prod=%q naive=%d",
						iter, i, p.Reason, n.Kind)
				}
			}
		}

		prodSnap := prod.SnapshotType(typeWidget)
		naiveSnap := naiveReg.SnapshotType(typeWidget)
		if !snapsEqual(prodSnap, naiveSnap) {
			t.Fatalf("iter=%d 依据: 最终全量状态与朴素模型不一致\nprod=%v\nnaive=%v",
				iter, prodSnap, naiveSnap)
		}
	}
}

// randomBatch 生成随机记录：
//   - 约 20% 概率制造字段类型错误；
//   - 小主键空间制造自然的同批次主键重复；
//   - 一半记录按 sumPreHook 合法序列（1,2,4,...），一半给随机错误 amount；
//   - 标签从小空间抽取以触发唯一钩子。
func randomBatch(rng *rand.Rand, n int, prefix string) []Record {
	out := make([]Record, 0, n)
	var sum int64
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("%s%d", prefix, rng.Intn(n+2))
		var amount int64
		if rng.Intn(2) == 0 {
			amount = sum + 1 // 合法：成功记录才会推进 sum，与钩子 Scratch 口径一致
		} else {
			amount = int64(10 + rng.Intn(90))
		}
		rec := Record{Type: typeWidget, ID: id}
		if rng.Intn(5) == 0 {
			rec.Fields = map[string]any{"amount": "bad-type", "label": fmt.Sprintf("lab%d", rng.Intn(3))}
			out = append(out, rec)
			continue
		}
		rec.Fields = map[string]any{"amount": amount, "label": fmt.Sprintf("lab%d", rng.Intn(3))}
		out = append(out, rec)
		if amount == sum+1 {
			sum += amount // 只有合法形状的记录可能通过前置钩子
		}
	}
	return out
}

// cloneInto 把 src 的当前全量状态复制到 dst（二者注册结构相同）。
func cloneInto(src, dst *Registry) {
	src.mu.Lock()
	defer src.mu.Unlock()
	dst.mu.Lock()
	defer dst.mu.Unlock()
	for name, insts := range src.data {
		if dst.data[name] == nil {
			dst.data[name] = map[string]Instance{}
		}
		for id, inst := range insts {
			dst.data[name][id] = inst.clone()
		}
	}
}

// applyNaiveState 用朴素模型的最终状态覆盖注册表内容。
func applyNaiveState(r *Registry, data map[string]map[string]Instance) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data = map[string]map[string]Instance{}
	for name, insts := range data {
		r.data[name] = map[string]Instance{}
		for id, inst := range insts {
			r.data[name][id] = inst.clone()
		}
	}
}

// kindOfReason 从归一化错误文本反推类别（测试仅用前缀判别）。
func kindOfReason(reason string) Kind {
	switch {
	case strings.HasPrefix(reason, "invalid parameter"):
		return KindInvalidParam
	case strings.HasPrefix(reason, "pre-hook rejected"):
		return KindPreHook
	case strings.HasPrefix(reason, "post-hook rejected"):
		return KindPostHook
	default:
		return 0
	}
}
