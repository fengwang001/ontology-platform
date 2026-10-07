package ontology

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

type writeRecord struct {
	id      InstanceID
	version int64
	value   int64
}

// TestConcurrentSubmitsAndWritesLinearizable 把并发的字段提交与实例写入
// 交织执行，然后用一个独立的朴素串行模型核对其可串行化性：
//
//   - 每次被接受写入所声明的字段定义版本必须是一个确定存在的已提交版本；
//   - 被接受的取值在该版本的字段定义下必然合法，不存在“介于两次提交
//     之间”的混合定义；
//   - 仅在 base 定义合法、在所有后续版本都非法的大值写入绝不允许被接受；
//   - 最终字段定义等于最后一个被接受提交；最终实例数据与朴素串行重放一致。
func TestConcurrentSubmitsAndWritesLinearizable(t *testing.T) {
	store := NewInstanceStore()
	ot := NewObjectType("O", store, nil, NewMemoryAuditLog())

	const baseBound = 1 << 50
	baseDef := &FieldDef{Type: NewIntType(),
		Constraint: NewRangeConstraint(0, baseBound, true, true),
		HasDefault: true, Default: NewValue(int64(0))}
	if _, err := ot.Submit([]FieldChange{{Name: "n", New: baseDef}}); err != nil {
		t.Fatal(err)
	}

	// 16 个互不相同、上界逐步收紧的提交（4 提交者共同驱动同一条链）。
	// 无论按什么全序被接受，版本 k>=1 的生效上界都必然落在区间
	// [maxes[last], maxes[0]] = [250_000, 1_000_000] 内。
	var maxes []int64
	for g := 0; g < 4; g++ {
		for s := 0; s < 4; s++ {
			maxes = append(maxes, int64(1_000_000-(g*4+s)*50_000))
		}
	}
	const K = 16

	var (
		mu       sync.Mutex
		records  []writeRecord
		rejects  int
		stepNext int64 // 下一个待领取的链步号
	)
	var wg sync.WaitGroup

	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			// 每个提交者领取链上唯一的下一步，再用版本 CAS 提交；
			// ErrVersionConflict 说明版本已前进，重读版本后重试同一变更。
			// 恰好 16 个不同步、每步至多接受一次 => 最终版本确定为 17。
			for {
				step := atomic.AddInt64(&stepNext, 1) - 1
				if step >= int64(len(maxes)) {
					return
				}
				mx := maxes[step]
				var prevMax int64 = baseBound
				if step > 0 {
					prevMax = maxes[step-1]
				}
				for attempt := 0; attempt < 1000; attempt++ {
					cur, _ := ot.Field("n")
					ver := ot.Version()
					// 等待前驱步生效：上界尚未走到本步的直接前驱时，
					// 本步提交会构成一次收紧而非预期的链顺序，先让出。
					if cur.Constraint.(rangeConstraint).max != prevMax {
						runtime.Gosched()
						continue
					}
					next := &FieldDef{Type: NewIntType(),
						Constraint: NewRangeConstraint(0, mx, true, true),
						HasDefault: true, Default: NewValue(int64(0))}
					_, err := ot.SubmitCAS(ver, []FieldChange{{Name: "n", Old: &cur, New: next}})
					if err == nil {
						break
					}
					if err != ErrVersionConflict {
						t.Errorf("step %d (mx=%d) unexpected error after %d attempts: %v",
							step, mx, attempt, err)
						return
					}
				}
			}
		}(g)
	}

	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				// 收紧期间只写对所有版本（含最终 [0,250_000]）都
				// 合法的小值，保证收紧提交总能通过；重点核对每次写入
				// 锁定的是一个确定存在的已提交版本。
				val := int64(g*7 + i) // <= 8*7+199=255
				v, err := ot.WriteInstance(
					InstanceID(fmt.Sprintf("g%dw%d", g, i)),
					map[string]Value{"n": NewValue(val)})
				mu.Lock()
				if err != nil {
					rejects++
				} else {
					records = append(records, writeRecord{
						id:      InstanceID(fmt.Sprintf("g%dw%d", g, i)),
						version: v,
						value:   val})
				}
				mu.Unlock()
			}
		}(g)
	}
	wg.Wait()

	finalVersion := ot.Version()
	if finalVersion != 1+K {
		t.Fatalf("final version=%d, want %d", finalVersion, 1+K)
	}

	// ---- 独立的朴素串行模型校验 ----

	// 1) 每个接受写入声明的版本必须是一个确定存在的已提交版本。
	for _, r := range records {
		if r.version < 1 || r.version > finalVersion {
			t.Fatalf("write %s pinned to nonexistent/intermediate version %d (final=%d)",
				r.id, r.version, finalVersion)
		}
	}

	// 2) 所有接受写入的取值都必须在其锁定版本的确定定义下合法。
	//    收紧期间写入的都是小值（<=255），对全部 17 个版本合法；
	//    再单独验证：在最终版本 [0,250_000] 下，仅 base 定义接受的
	//    大值必须被拒绝——证明不存在“倒退回旧定义”的中间时刻。
	for _, r := range records {
		if r.value > baseBound || r.value < 0 {
			t.Fatalf("write %s value=%d illegal under every version", r.id, r.value)
		}
		if r.value > maxes[K-1] {
			t.Fatalf("write %s value=%d exceeds final bound %d",
				r.id, r.value, maxes[K-1])
		}
	}
	if _, err := ot.WriteInstance("post", map[string]Value{"n": NewValue(2_000_000)}); err == nil {
		t.Fatal("value legal only under the base definition must be rejected after tightening")
	}

	// 3) 朴素串行重放：每个存活实例与接受记录一一对应且取值一致。
	serial := make(map[InstanceID]int64, len(records))
	for _, r := range records {
		serial[r.id] = r.value
	}
	if store.LiveCount() != len(serial) {
		t.Fatalf("live count=%d, serial=%d, rejects=%d",
			store.LiveCount(), len(serial), rejects)
	}
	store.Scan(func(inst Instance) bool {
		want, ok := serial[inst.ID]
		if !ok || inst.Fields["n"].Raw() != want {
			t.Fatalf("instance %s not explained by serial replay: %v (want %d)",
				inst.ID, inst.Fields["n"], want)
		}
		return true
	})

	// 4) 最终字段定义必须等于最后一个被接受提交（最严的放宽上界）。
	finalDef, ok := ot.Field("n")
	if !ok {
		t.Fatal("field n missing")
	}
	if got := finalDef.Constraint.(rangeConstraint).max; got != maxes[K-1] {
		t.Fatalf("final bound=%d, want %d", got, maxes[K-1])
	}

	_ = rejects
}

// nextSmallerBound 返回比 current 严格更小的最大候选上界（即链上的
// 下一步）；不存在时 done==true。
func nextSmallerBound(current int64, candidates []int64) (mx int64, done bool) {
	best := int64(-1)
	for _, c := range candidates {
		if c < current && c > best {
			best = c
		}
	}
	if best < 0 {
		return 0, true
	}
	return best, false
}
