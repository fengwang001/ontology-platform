// Package difftest 用随机操作序列把优化实现(router+migration+backfill)
// 与朴素参考模型(naive)逐条对照:朴素模型独立维护全部实例的当前实际
// 版本与全部历史对应关系,每次读取都重新计算;两个系统在任意随机读写、
// 回填与迁移声明变更序列下必须产生完全一致的结果。
package difftest

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"

	"ontology/ontology/backfill"
	"ontology/ontology/migration"
	"ontology/ontology/naive"
	"ontology/ontology/router"
)

// errKind 把两个实现各自的错误哨兵归一化为可比较的错误类别。
type errKind int

const (
	errNone errKind = iota
	errInvalid
	errNotFound
)

func (k errKind) String() string {
	switch k {
	case errNone:
		return "ok"
	case errInvalid:
		return "invalid"
	case errNotFound:
		return "not-found"
	default:
		return "?"
	}
}

func kindOf(err error) errKind {
	switch {
	case err == nil:
		return errNone
	case errors.Is(err, router.ErrInvalidArgument), errors.Is(err, naive.ErrInvalid):
		return errInvalid
	case errors.Is(err, router.ErrNotFound), errors.Is(err, naive.ErrNotFound):
		return errNotFound
	default:
		panic(fmt.Sprintf("unclassified error: %v", err))
	}
}

var (
	propPool = []string{"p0", "p1", "p2", "p3", "p4", "p5"}
	idPool   = []string{"i0", "i1", "i2", "i3", "i4", "i5", "i6", "i7"}
)

// op 是一条随机生成的操作。
type op struct {
	desc     string
	applyOpt func() string // 应用到优化实现,返回可比较的结果摘要
	applyRef func() string // 应用到朴素模型,返回可比较的结果摘要
}

func randVersion(r *rand.Rand) router.Version {
	if r.Intn(2) == 0 {
		return router.VersionOld
	}
	return router.VersionNew
}

func toNaiveVersion(v router.Version) naive.Version {
	if v == router.VersionOld {
		return naive.Old
	}
	return naive.New
}

func randProps(r *rand.Rand) map[string]any {
	n := 1 + r.Intn(3)
	props := make(map[string]any, n)
	for i := 0; i < n; i++ {
		props[propPool[r.Intn(len(propPool))]] = r.Intn(100)
	}
	return props
}

func randMappings(r *rand.Rand) []migration.Mapping {
	n := 1 + r.Intn(3)
	out := make([]migration.Mapping, 0, n)
	for i := 0; i < n; i++ {
		p := propPool[r.Intn(len(propPool))]
		switch r.Intn(3) {
		case 0:
			out = append(out, migration.Mapping{Property: p, Action: migration.ActionRetain})
		case 1:
			out = append(out, migration.Mapping{Property: p, Action: migration.ActionDeprecate})
		default:
			out = append(out, migration.Mapping{Property: p, Action: migration.ActionAddDefault, Default: r.Intn(10)})
		}
	}
	// 小概率注入自相矛盾的批次,覆盖拒绝路径。
	if r.Intn(10) == 0 && len(out) > 0 {
		out = append(out, migration.Mapping{Property: out[0].Property, Action: migration.ActionDeprecate})
		out = append(out, migration.Mapping{Property: out[0].Property, Action: migration.ActionRetain})
	}
	return out
}

func toNaiveMappings(ms []migration.Mapping) []naive.Mapping {
	out := make([]naive.Mapping, 0, len(ms))
	for _, m := range ms {
		var a naive.Action
		switch m.Action {
		case migration.ActionRetain:
			a = naive.Retain
		case migration.ActionDeprecate:
			a = naive.Deprecate
		case migration.ActionAddDefault:
			a = naive.AddDefault
		}
		out = append(out, naive.Mapping{Property: m.Property, Action: a, Default: m.Default})
	}
	return out
}

func viewString(m map[string]any, err error) string {
	if err != nil {
		return kindOf(err).String()
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	s := "{"
	for i, k := range keys {
		if i > 0 {
			s += " "
		}
		s += fmt.Sprintf("%s:%v", k, m[k])
	}
	return s + "}"
}

// genOps 生成确定性的随机操作序列(同一种子必然生成同一序列)。
func genOps(r *rand.Rand, svc *router.Service, w *backfill.Worker, ref *naive.Model, count int) []op {
	ops := make([]op, 0, count)
	for i := 0; i < count; i++ {
		id := idPool[r.Intn(len(idPool))]
		switch r.Intn(100) {
		case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14: // create 15%
			v := randVersion(r)
			props := randProps(r)
			ops = append(ops, op{
				desc:     fmt.Sprintf("create(%s, %s, %v)", id, v, props),
				applyOpt: func() string { return kindOf(svc.Create(id, v, props)).String() },
				applyRef: func() string { return kindOf(ref.Create(id, toNaiveVersion(v), props)).String() },
			})
		case 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39: // read 25%
			v := randVersion(r)
			ops = append(ops, op{
				desc:     fmt.Sprintf("read(%s, %s)", id, v),
				applyOpt: func() string { return viewString(svc.Read(id, v)) },
				applyRef: func() string { return viewString(ref.Read(id, toNaiveVersion(v))) },
			})
		case 40, 41, 42, 43, 44, 45, 46, 47, 48, 49, 50, 51, 52, 53, 54, 55, 56, 57, 58, 59, 60, 61, 62, 63, 64: // write 25%
			v := randVersion(r)
			props := randProps(r)
			ops = append(ops, op{
				desc:     fmt.Sprintf("write(%s, %s, %v)", id, v, props),
				applyOpt: func() string { return kindOf(svc.Write(id, v, props)).String() },
				applyRef: func() string { return kindOf(ref.Write(id, toNaiveVersion(v), props)).String() },
			})
		case 65, 66, 67, 68, 69, 70, 71, 72: // delete 8%
			ops = append(ops, op{
				desc:     fmt.Sprintf("delete(%s)", id),
				applyOpt: func() string { return kindOf(svc.Delete(id)).String() },
				applyRef: func() string { return kindOf(ref.Delete(id)).String() },
			})
		case 73, 74, 75, 76, 77, 78, 79, 80, 81, 82, 83, 84: // amend 12%
			ms := randMappings(r)
			ops = append(ops, op{
				desc:     fmt.Sprintf("amend(%v)", ms),
				applyOpt: func() string { return kindOf(svc.AmendDeclaration(ms)).String() },
				applyRef: func() string { return kindOf(ref.Amend(toNaiveMappings(ms))).String() },
			})
		default: // backfill step 15%
			ops = append(ops, op{
				desc: "backfill-step()",
				applyOpt: func() string {
					r := w.Step()
					if r.Done {
						return "done"
					}
					return fmt.Sprintf("%s/%v", r.ID, r.Outcome)
				},
				applyRef: func() string {
					bid, done := ref.BackfillNext()
					if done {
						return "done"
					}
					return fmt.Sprintf("%s/applied", bid)
				},
			})
		}
	}
	return ops
}

// dumpState 汇总全部实例在两个版本下的可见状态,用于逐步对照。
func dumpState(svc *router.Service, ref *naive.Model) (string, string) {
	dump := func(read func(id string, v router.Version) (map[string]any, error), status func(id string) (bool, bool), ids []string) string {
		s := ""
		for _, id := range ids {
			_, migrated := status(id)
			oldView, _ := read(id, router.VersionOld)
			newView, _ := read(id, router.VersionNew)
			s += fmt.Sprintf("%s[migrated=%v old=%s new=%s] ", id, migrated, viewString(oldView, nil), viewString(newView, nil))
		}
		return s
	}
	opt := dump(svc.Read, svc.Status, svc.InstanceIDs())
	reference := dump(
		func(id string, v router.Version) (map[string]any, error) { return ref.Read(id, toNaiveVersion(v)) },
		ref.Status, ref.IDs())
	return opt, reference
}

// runScenario 把同一操作序列应用到两个实现并逐条对照,返回每步日志。
func runScenario(t *testing.T, seed int64, opCount int, verbose bool) []string {
	t.Helper()
	r := rand.New(rand.NewSource(seed))
	svc := router.NewService(migration.NewDeclaration())
	w := backfill.NewWorker(svc)
	ref := naive.NewModel()

	ops := genOps(r, svc, w, ref, opCount)
	logs := make([]string, 0, len(ops))
	for i, o := range ops {
		gotOpt := o.applyOpt()
		gotRef := o.applyRef()
		line := fmt.Sprintf("seed=%d op#%d 输入: %s | 实际输出: %s | 判定依据: 朴素模型重放全部历史对应关系后输出: %s",
			seed, i, o.desc, gotOpt, gotRef)
		logs = append(logs, line)
		if verbose {
			t.Log(line)
		}
		if gotOpt != gotRef {
			t.Fatalf("op#%d %s: optimized=%s naive=%s", i, o.desc, gotOpt, gotRef)
		}
		// 每 32 步做一次全量状态对照,精确定位发散点。
		if i%32 == 31 {
			optState, refState := dumpState(svc, ref)
			if optState != refState {
				t.Fatalf("state diverged after op#%d\noptimized: %s\nnaive:     %s", i, optState, refState)
			}
		}
	}
	optState, refState := dumpState(svc, ref)
	if optState != refState {
		t.Fatalf("final state diverged\noptimized: %s\nnaive:     %s", optState, refState)
	}
	return logs
}

// TestDifferentialRandomSequences 在多个种子下做随机对照。
func TestDifferentialRandomSequences(t *testing.T) {
	for _, seed := range []int64{1, 7, 42, 1337, 20261007} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			logs := runScenario(t, seed, 600, true)
			t.Logf("最终对照通过: %d 条操作逐条一致, 末态一致", len(logs))
		})
	}
}

// TestReplayDeterminism 重放同一组操作与回填触发序列,
// 必须得到完全相同的最终状态与每次读取结果。
func TestReplayDeterminism(t *testing.T) {
	const seed = 99
	first := runScenario(t, seed, 400, false)
	second := runScenario(t, seed, 400, false)
	if !reflect.DeepEqual(first, second) {
		t.Fatal("replaying the same op sequence produced different results")
	}
	t.Logf("依据: 同一种子重放两次, %d 条操作结果逐字节一致", len(first))
}

// TestNaiveCostGrowsWithHistory 对照证明:朴素模型每次读取都重放全部历史
// 对应关系,开销随历史变更次数增长;优化实现的现算开销则与之无关
// (优化侧的证明见 migration.TestForwardViewCostIndependentOfAmendments)。
func TestNaiveCostGrowsWithHistory(t *testing.T) {
	ref := naive.NewModel()
	if err := ref.Create("a", naive.Old, map[string]any{"p0": 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := ref.Read("a", naive.New); err != nil {
		t.Fatal(err)
	}
	before := ref.LastOps()
	for i := 0; i < 500; i++ {
		if err := ref.Amend([]naive.Mapping{{Property: fmt.Sprintf("junk%d", i), Action: naive.Deprecate}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ref.Read("a", naive.New); err != nil {
		t.Fatal(err)
	}
	after := ref.LastOps()
	t.Logf("输入: 追加 500 次历史变更后读取; 朴素模型单次读取重放代价: %d -> %d", before, after)
	if after <= before {
		t.Fatalf("naive model should replay all history per read: before=%d after=%d", before, after)
	}
}
