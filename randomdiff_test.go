package ontology_test

import (
	"fmt"
	"math/rand"
	"testing"

	"ontology"
	"ontology/naive"
)

// op 是差分测试的统一操作表示，可被真实子系统与朴素模型分别应用，
// 也可被序列化重放（同序列重放必须得到完全相同的各聚合取值）。
type op struct {
	kind             int // 0=write 1=delete
	key              string
	prev             int64
	amount           float64
	region, category string
}

func (o op) String() string {
	if o.kind == 1 {
		return fmt.Sprintf("Delete key=%s prev=%d", o.key, o.prev)
	}
	return fmt.Sprintf("Write key=%s prev=%d amount=%v region=%s category=%s",
		o.key, o.prev, o.amount, o.region, o.category)
}

// apply 分别在真实子系统与朴素模型上应用同一操作，并逐条对照
// （错误类别、新版本号、删除标记、两个视图的全部相关分组）。
// 每次操作都打印输入、两边实际输出与判定依据。
func applyAndCompare(t *testing.T, opno int, o op, k *ontology.Kernel, m *naive.Model, probes []string) {
	t.Helper()
	if o.kind == 0 {
		w := ontology.Write{Type: typeOrder, Key: o.key, Prev: o.prev,
			Attrs: attrs(o.amount, o.region, o.category)}
		r1, e1 := k.Write(w)
		r2, e2 := m.Write(w)
		basis := fmt.Sprintf("错误类别(%s vs %s)与成功回执(v=%d vs v=%d, sn=%d vs sn=%d)必须一致",
			kindOf(e1), kindOf(e2), r1.Version, r2.Version, r1.CommitSN, r2.CommitSN)
		trace(t, opno, o.String(),
			fmt.Sprintf("kernel={v:%d del:%v sn:%d err:%s} naive={v:%d del:%v sn:%d err:%s}",
				r1.Version, r1.Deleted, r1.CommitSN, errLabel(e1),
				r2.Version, r2.Deleted, r2.CommitSN, errLabel(e2)),
			basis)
		if kindOf(e1) != kindOf(e2) || r1.Version != r2.Version || r1.Deleted != r2.Deleted || r1.CommitSN != r2.CommitSN {
			t.Fatalf("divergence on write %q: kernel=%+v/%v naive=%+v/%v", o.key, r1, e1, r2, e2)
		}
	} else {
		d := ontology.Delete{Type: typeOrder, Key: o.key, Prev: o.prev}
		r1, e1 := k.Delete(d)
		r2, e2 := m.Delete(d)
		basis := fmt.Sprintf("删除错误类别(%s vs %s)：not_found/already_deleted/conflict 必须一致，成功时版本与 sn 一致",
			kindOf(e1), kindOf(e2))
		trace(t, opno, o.String(),
			fmt.Sprintf("kernel={v:%d del:%v err:%s} naive={v:%d del:%v err:%s}",
				r1.Version, r1.Deleted, errLabel(e1), r2.Version, r2.Deleted, errLabel(e2)),
			basis)
		if kindOf(e1) != kindOf(e2) || r1.Version != r2.Version || r1.Deleted != r2.Deleted {
			t.Fatalf("divergence on delete %q: kernel=%+v/%v naive=%+v/%v", o.key, r1, e1, r2, e2)
		}
	}

	// 查询对照：对若干探测分组（含本次可能触及的分组），
	// 真实增量索引与朴素全表重算的 SUM/成员数必须相等。
	probes = append(probes, o.region, o.category)
	compared := 0
	for _, view := range []string{viewByRegion, viewByCategory} {
		seen := map[string]bool{}
		for _, g := range probes {
			if g == "" || seen[g] {
				continue
			}
			seen[g] = true
			got, meter, qerr := k.Query(view, g)
			want, _, nerr := m.Query(view, g)
			compared++
			if qerr != nil || nerr != nil || got.Sum != want.Sum || got.Members != want.Members {
				t.Fatalf("query divergence view=%s group=%s: kernel=%+v(%v) naive=%+v",
					view, g, got, meter, want)
			}
		}
	}

	// 整视图级别对照：朴素模型全量重算 vs 真实子系统全量重算（同口径），
	// 再抽查增量索引的随机分组，三重保证「视图是源实例纯粹派生物」。
	for _, view := range []string{viewByRegion, viewByCategory} {
		full := m.RecomputeView(view)
		recomputed := k.RecomputeView(view)
		if len(full) != len(recomputed) {
			t.Fatalf("view %s group count mismatch: %d vs %d", view, len(full), len(recomputed))
		}
		for g, v := range full {
			if recomputed[g] != v {
				t.Fatalf("view %s group %s mismatch: %+v vs %+v", view, g, recomputed[g], v)
			}
			live, _, _ := k.Query(view, g)
			if live.Sum != v.Sum || live.Members != v.Members {
				t.Fatalf("incremental index stale for %s/%s: %+v vs recomputed %+v", view, g, live, v)
			}
		}
	}
	trace(t, opno, "全量对照(增量索引 vs 重算 vs 朴素重扫)",
		fmt.Sprintf("探测分组查询 %d 个 + 两个视图全部分组一致", compared),
		"任意时刻增量索引 == 用当时最新可见版本重新计算 == 朴素全表扫描结果")
}

// genOps 用固定种子生成大量随机写入/删除/分组迁移操作。
// prev 有一定概率故意写错（触发版本冲突），key 有一定概率是从未出现过的。
func genOps(rng *rand.Rand, n, keySpace int) []op {
	ops := make([]op, 0, n)
	regions := []string{"us", "eu", "cn", "jp", ""}    // 末位偶尔触发空分组键非法
	categories := []string{"book", "game", "food", ""} // 同上
	versionOf := map[string]int64{}
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("k%d", rng.Intn(keySpace))
		current := versionOf[key] // 朴素/内核视角一致：只有成功提交才推进
		if rng.Intn(6) == 0 && current > 0 {
			prev := current
			if rng.Intn(3) == 0 {
				prev = current - 1 // 故意过期
			}
			ops = append(ops, op{kind: 1, key: key, prev: prev})
			if prev == current {
				versionOf[key] = current + 1 // 删除成功 -> 墓碑版本
			}
			continue
		}
		prev := current
		if rng.Intn(4) == 0 {
			prev = rng.Int63n(5) // 随机凭证，制造冲突
		}
		o := op{
			kind: 0, key: key, prev: prev,
			amount:   float64(rng.Intn(201) - 100),
			region:   regions[rng.Intn(len(regions))],
			category: categories[rng.Intn(len(categories))],
		}
		ops = append(ops, o)
		if prev == current {
			// 参数可能非法（空分组键），仅当非空才算成功推进版本。
			if o.region != "" && o.category != "" {
				versionOf[key] = current + 1
			}
		}
	}
	return ops
}

func TestRandomDifferential(t *testing.T) {
	const seed = int64(20261007)
	const n = 400
	const keySpace = 25
	t.Logf("random differential seed=%d ops=%d keySpace=%d", seed, n, keySpace)
	fmt.Printf("=== TestRandomDifferential seed=%d ops=%d ===\n", seed, n)
	rng := rand.New(rand.NewSource(seed))
	ops := genOps(rng, n, keySpace)

	run := func() (*ontology.Kernel, *naive.Model) {
		k, m := newKernel(t), newNaive(t)
		probes := []string{}
		for i, o := range ops {
			applyAndCompare(t, i+1, o, k, m, probes)
			probes = append(probes, o.region, o.category)
		}
		return k, m
	}

	k1, m1 := run()
	// 重放同一组操作序列到全新实例：必须得到完全相同的各聚合取值与版本状态。
	k2, _ := run()
	for _, view := range []string{viewByRegion, viewByCategory} {
		a, b := k1.RecomputeView(view), k2.RecomputeView(view)
		if len(a) != len(b) {
			t.Fatalf("replay view %s group count differs: %d vs %d", view, len(a), len(b))
		}
		for g, v := range a {
			if b[g] != v {
				t.Fatalf("replay nondeterministic for %s/%s: %+v vs %+v", view, g, v, b[g])
			}
		}
	}
	// 朴素模型与真实子系统最终状态一致。
	for _, view := range []string{viewByRegion, viewByCategory} {
		full := m1.RecomputeView(view)
		for g, v := range full {
			got, _, _ := k1.Query(view, g)
			if got.Sum != v.Sum || got.Members != v.Members {
				t.Fatalf("final mismatch %s/%s: %+v vs %+v", view, g, got, v)
			}
		}
	}
	fmt.Printf("=== TestRandomDifferential PASS: %d ops, replay identical ===\n", n)
}
