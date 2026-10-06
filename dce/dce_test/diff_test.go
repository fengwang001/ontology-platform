package dce_test

import (
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strings"
	"testing"

	"ontology/dce/analyzer"
	"ontology/dce/dceerr"
	"ontology/dce/model"
	"ontology/dce/registry"
)

// genGraph 生成保证通过构造期检查（无未知模块/未定义引用）的随机模块图，
// 但允许出现缺失/歧义/重导出环等解析期情形。
func genGraph(rng *rand.Rand, n int) (map[string]*model.Module, []string) {
	ids := make([]string, n)
	mods := map[string]*model.Module{}
	for i := range ids {
		ids[i] = fmt.Sprintf("m%02d", i)
		mods[ids[i]] = &model.Module{ID: ids[i]}
	}

	declPool := map[string][]string{}
	localBindings := map[string]map[string]string{} // module -> local -> imported

	for _, id := range ids {
		m := mods[id]
		switch rng.Intn(4) {
		case 0:
			m.SideEffect = model.SideEffectNo
		default:
			m.SideEffect = model.SideEffectYes
		}

		nd := 1 + rng.Intn(4)
		nameSet := map[string]bool{}
		for i := 0; i < nd; i++ {
			name := fmt.Sprintf("d%d", i)
			nameSet[name] = true
			se := rng.Intn(3) == 0
			m.Decls = append(m.Decls, model.Declaration{Name: name, SideEffect: se})
		}
		declPool[id] = mapKeys2(nameSet)
		localBindings[id] = map[string]string{}

		// 导入：可能纯副作用、具名、通配。
		ni := rng.Intn(3)
		for i := 0; i < ni; i++ {
			target := ids[rng.Intn(n)]
			imp := model.Import{Target: target}
			switch rng.Intn(4) {
			case 0:
				// 纯副作用导入
			case 1:
				local := fmt.Sprintf("ns%d", i)
				imp.Bindings = append(imp.Bindings, model.ImportBinding{Local: local, Imported: "*"})
				localBindings[id][local] = "*"
			default:
				nb := 1 + rng.Intn(2)
				for j := 0; j < nb; j++ {
					local := fmt.Sprintf("i%d_%d", i, j)
					imported := []string{"d0", "d1", "x", "default", "missing"}[rng.Intn(5)]
					imp.Bindings = append(imp.Bindings, model.ImportBinding{Local: local, Imported: imported})
					localBindings[id][local] = imported
				}
			}
			m.Imports = append(m.Imports, imp)
		}

		// 导出：本地导出 + 少量重导出/通配重导出。
		for _, d := range declPool[id] {
			if rng.Intn(2) == 0 {
				m.Exports = append(m.Exports, localExp(d))
			}
		}
		if rng.Intn(2) == 0 && n > 1 {
			target := ids[rng.Intn(n)]
			switch rng.Intn(3) {
			case 0:
				m.Exports = append(m.Exports, starExp(target))
			default:
				foreign := []string{"d0", "d1", "x"}[rng.Intn(3)]
				name := foreign
				if rng.Intn(2) == 0 {
					name = "x"
				}
				if !hasExportName(m, name) {
					m.Exports = append(m.Exports, namedExp(name, target, foreign))
				}
			}
		}

		// 补齐引用：只引用本地声明或已存在的导入本地名，保证类别 4 通过。
		for i := range m.Decls {
			var pool []string
			pool = append(pool, declPool[id]...)
			for local := range localBindings[id] {
				pool = append(pool, local)
			}
			nr := rng.Intn(3)
			for j := 0; j < nr && len(pool) > 0; j++ {
				m.Decls[i].Refs = append(m.Decls[i].Refs, pool[rng.Intn(len(pool))])
			}
		}
	}

	entries := []string{ids[rng.Intn(n)]}
	if rng.Intn(2) == 0 && n > 1 {
		entries = append(entries, ids[rng.Intn(n)])
	}
	return mods, dedupStr(entries)
}

func mapKeys2(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func dedupStr(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func hasExportName(m *model.Module, name string) bool {
	for _, e := range m.Exports {
		if e.Kind == model.ExportLocal && e.LocalName == name {
			return true
		}
		if e.Kind == model.ExportReexport && e.ExportName == name {
			return true
		}
	}
	return false
}

func TestRandomDifferential(t *testing.T) {
	var logFile *os.File
	if os.Getenv("DCE_DIFF_LOG") != "" {
		f, err := os.Create(os.Getenv("DCE_DIFF_LOG"))
		if err != nil {
			t.Fatal(err)
		}
		logFile = f
		defer f.Close()
	}

	for iter := 0; iter < 400; iter++ {
		rng := rand.New(rand.NewSource(int64(iter)*1000003 + 7))
		n := 2 + rng.Intn(7)
		mods, entries := genGraph(rng, n)

		naive := naiveSolve(mods, entries)

		reg := newRegistryFromMap(mods)
		sess := reg.NewSession(entries)
		var buf strings.Builder
		res, err := sess.Solve(analyzer.WithLogger(analyzer.NewTextLogger(&buf)))

		if logFile != nil {
			fmt.Fprintf(logFile, "===== iter %d =====\n%s\n", iter, dumpGraph(mods, entries))
			if err != nil {
				fmt.Fprintf(logFile, "engine error: %v\n", err)
			} else {
				for _, k := range res.Kept {
					fmt.Fprintf(logFile, "KEEP %s/%s %s\n", k.Key.Module, k.Key.Decl, k.Reason)
				}
				fmt.Fprintf(logFile, "INCLUDED %v\n", res.Included)
			}
		}

		if naive.errCat != 0 {
			if err == nil {
				t.Fatalf("iter %d: naive expects error %s, engine succeeded\n%s", iter, naive.errCat, dumpGraph(mods, entries))
			}
			e, ok := dceerr.As(err)
			if !ok || e.Category != naive.errCat {
				t.Fatalf("iter %d: error category mismatch naive=%s engine=%v\n%s",
					iter, naive.errCat, err, dumpGraph(mods, entries))
			}
			continue
		}
		if err != nil {
			t.Fatalf("iter %d: naive succeeded but engine error %v\n%s", iter, err, dumpGraph(mods, entries))
		}

		// 包含集合。
		wantIncluded := mapKeys(naive.included)
		sort.Strings(wantIncluded)
		if !eqStrings(wantIncluded, res.Included) {
			t.Fatalf("iter %d: included mismatch\nnaive=%v\nengine=%v\n%s",
				iter, wantIncluded, res.Included, dumpGraph(mods, entries))
		}

		// 保留集合与原因。
		if len(res.Kept) != len(naive.kept) {
			t.Fatalf("iter %d: kept size mismatch naive=%d engine=%d\n%s",
				iter, len(naive.kept), len(res.Kept), dumpGraph(mods, entries))
		}
		for _, k := range res.Kept {
			want, ok := naive.kept[k.Key]
			if !ok {
				t.Fatalf("iter %d: engine keeps %s/%s but naive does not\n%s",
					iter, k.Key.Module, k.Key.Decl, dumpGraph(mods, entries))
			}
			if reasonString(want) != k.Reason.String() {
				t.Fatalf("iter %d: reason mismatch for %s/%s naive=%s engine=%s\n%s",
					iter, k.Key.Module, k.Key.Decl, reasonString(want), k.Reason, dumpGraph(mods, entries))
			}
		}

		// 近线性可验证性：每个（模块,声明）至多处理一次；模块导入只扫描一次。
		if res.Stats.DeclProcessed > totalDecls(mods) {
			t.Fatalf("iter %d: decl processed %d > total %d", iter, res.Stats.DeclProcessed, totalDecls(mods))
		}
		if res.Stats.ModulesScanned > len(mods) {
			t.Fatalf("iter %d: modules scanned %d > %d", iter, res.Stats.ModulesScanned, len(mods))
		}
		if res.ResolverStats.NamedResolves > totalNameSlots(mods) {
			t.Fatalf("iter %d: named resolves %d > name slots %d",
				iter, res.ResolverStats.NamedResolves, totalNameSlots(mods))
		}
	}
}

func newRegistryFromMap(mods map[string]*model.Module) *registry.Registry {
	reg := registry.New()
	ids := make([]string, 0, len(mods))
	for id := range mods {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if err := reg.Register(mods[id]); err != nil {
			panic(err)
		}
	}
	return reg
}

func eqStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func totalDecls(mods map[string]*model.Module) int {
	n := 0
	for _, m := range mods {
		n += len(m.Decls)
	}
	return n
}

func totalNameSlots(mods map[string]*model.Module) int {
	// 上界：模块数 × 名字宇宙规模（每个模块出现的不同名字总数，粗上界）。
	nameUniverse := map[string]bool{}
	for _, m := range mods {
		for _, e := range m.Exports {
			if e.Kind == model.ExportLocal {
				nameUniverse[e.LocalName] = true
			}
			if e.Kind == model.ExportReexport {
				nameUniverse[e.ExportName] = true
				nameUniverse[e.ForeignName] = true
			}
		}
	}
	return len(mods) * (len(nameUniverse) + 1)
}
