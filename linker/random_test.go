package linker

import (
	"errors"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
)

var diffLog = flag.String("difflog", "", "随机对照测试日志输出目录（为空则仅在失败时打印）")

// genWorld 为一次对照试验的输入：一组 AddModule 操作与随后的 Evaluate 序列。
type genWorld struct {
	modules []ModuleDef
	roots   []string
}

// genValidModule 生成一个通过 AddModule 校验的模块。
// 依赖与导入只能引用 earlier 中已经确定存在（或允许未来/幽灵）的名字。
func genValidModule(rng *rand.Rand, idx int, names []string) ModuleDef {
	name := fmt.Sprintf("m%02d", idx)
	d := ModuleDef{Name: name}

	nExports := rng.Intn(4)
	exportNames := map[string]bool{}
	for i := 0; i < nExports; i++ {
		en := fmt.Sprintf("e%d", i)
		exportNames[en] = true
		kind := KindFunction
		if rng.Intn(2) == 0 {
			kind = KindLet
		}
		d.Exports = append(d.Exports, Export{Name: en, Kind: kind})
	}

	usedImport := map[string]bool{}
	for en := range exportNames {
		usedImport[en] = true
	}

	// 依赖（来源）：从已有模块中选，可能引用尚未生成的未来名字或幽灵名字。
	nImports := rng.Intn(4)
	for i := 0; i < nImports; i++ {
		var src string
		switch {
		case len(names) > 0 && rng.Intn(100) < 70:
			src = names[rng.Intn(len(names))]
		case rng.Intn(2) == 0:
			src = fmt.Sprintf("future%02d", rng.Intn(6))
		default:
			src = "ghost" + itoa(rng.Intn(4))
		}
		if src == name {
			continue
		}
		nb := 1 + rng.Intn(3)
		var binds []string
		for j := 0; j < nb; j++ {
			bn := fmt.Sprintf("imp%d_%d", i, j)
			if usedImport[bn] {
				continue
			}
			usedImport[bn] = true
			binds = append(binds, bn)
		}
		if len(binds) > 0 {
			d.Imports = append(d.Imports, ImportStmt{Source: src, Bindings: binds})
		}
	}

	// 转出：具名或星，引用已有/幽灵模块。
	nRe := rng.Intn(3)
	for i := 0; i < nRe; i++ {
		src := pickSource(rng, names, i)
		if src == name {
			continue
		}
		if rng.Intn(2) == 0 {
			rn := fmt.Sprintf("re%d", i)
			if usedImport[rn] || exportNames[rn] {
				continue
			}
			usedImport[rn] = true
			d.ReExports = append(d.ReExports, namedX(rn, src, pickExportName(rng)))
		} else {
			d.ReExports = append(d.ReExports, starX(src))
		}
	}

	// 体：对本地 let 做 Init；读本地或导入绑定；偶尔 Throw。
	var lets []string
	for _, e := range d.Exports {
		if e.Kind == KindLet {
			lets = append(lets, e.Name)
		}
	}
	nSteps := rng.Intn(6)
	inited := map[string]bool{}
	for i := 0; i < nSteps; i++ {
		switch rng.Intn(4) {
		case 0:
			if len(lets) > 0 {
				x := lets[rng.Intn(len(lets))]
				if !inited[x] {
					d.Body = append(d.Body, Init(x))
					inited[x] = true
				}
			}
		case 1:
			if len(d.Exports) > 0 {
				e := d.Exports[rng.Intn(len(d.Exports))]
				d.Body = append(d.Body, Read(name, e.Name))
			}
		case 2:
			if len(d.Imports) > 0 {
				st := d.Imports[rng.Intn(len(d.Imports))]
				bn := st.Bindings[rng.Intn(len(st.Bindings))]
				d.Body = append(d.Body, Read(st.Source, bn))
			}
		default:
			d.Body = append(d.Body, Throw(fmt.Sprintf("err %d-%d", idx, i)))
		}
	}
	return d
}

func pickSource(rng *rand.Rand, names []string, salt int) string {
	if len(names) > 0 && rng.Intn(100) < 70 {
		return names[rng.Intn(len(names))]
	}
	if rng.Intn(2) == 0 {
		return fmt.Sprintf("future%02d", rng.Intn(6))
	}
	return "gstar" + itoa(salt)
}

func pickExportName(rng *rand.Rand) string {
	names := []string{"e0", "e1", "e2", "v", "w", "missing"}
	return names[rng.Intn(len(names))]
}

func generateWorld(rng *rand.Rand) genWorld {
	n := 3 + rng.Intn(14)
	w := genWorld{}
	names := make([]string, 0, n)
	for i := 0; i < n; i++ {
		d := genValidModule(rng, i, names)
		w.modules = append(w.modules, d)
		names = append(names, d.Name)
	}
	// 额外登记一部分 future 模块（名字匹配 futureNN），让部分悬空依赖落地。
	for i := 0; i < rng.Intn(5); i++ {
		fn := fmt.Sprintf("future%02d", i)
		w.modules = append(w.modules, ModuleDef{
			Name:    fn,
			Exports: []Export{{"e0", KindLet}, {"v", KindFunction}},
			Body:    []Step{Init("e0")},
		})
	}
	for i := 0; i < 2+rng.Intn(4); i++ {
		w.roots = append(w.roots, names[rng.Intn(len(names))])
	}
	// 偶尔对不存在或非法的根求值。
	if rng.Intn(3) == 0 {
		w.roots = append(w.roots, "ghost0")
	}
	return w
}

type worldSnapshot struct {
	status map[string]string
	lets   map[string]map[string]bool
	order  []string
}

func snapshotSession(s *Session) worldSnapshot {
	snap := worldSnapshot{status: map[string]string{}, lets: map[string]map[string]bool{}, order: s.Order()}
	for name, m := range s.modules {
		st, _ := s.Status(name)
		snap.status[name] = st.Status.String() + ":" + st.Error
		lm := map[string]bool{}
		for k, v := range st.Lets {
			lm[k] = v
		}
		snap.lets[name] = lm
		_ = m
	}
	return snap
}

func snapshotNaive(n *naiveSim) worldSnapshot {
	snap := worldSnapshot{status: map[string]string{}, lets: map[string]map[string]bool{}, order: n.orderSnapshot()}
	for name := range n.mods {
		st, _ := n.status(name)
		snap.status[name] = st.Status.String() + ":" + st.Error
		lm := map[string]bool{}
		for k, v := range st.Lets {
			lm[k] = v
		}
		snap.lets[name] = lm
	}
	return snap
}

func errClass(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrInvalidArg):
		return "invalid"
	case errors.Is(err, ErrNameExists):
		return "exists"
	case errors.Is(err, ErrTooMany):
		return "toomany"
	case errors.Is(err, ErrNotFound):
		return "notfound"
	}
	if le, ok := AsLinkError(err); ok {
		if le.Ambiguous {
			return "ambig:" + le.Module + "," + le.Source + "," + le.Name
		}
		return "linkerr:" + le.Module + "," + le.Source + "," + le.Name
	}
	return err.Error()
}

func resultKey(r EvalResult) string {
	if r.OK {
		return fmt.Sprintf("ok appended=%v", r.Appended)
	}
	return fmt.Sprintf("err %q appended=%v", r.Error, r.Appended)
}

func naiveResultKey(r naiveResult) string {
	if r.ok {
		return fmt.Sprintf("ok appended=%v", r.appended)
	}
	return fmt.Sprintf("err %q appended=%v", r.err, r.appended)
}

func runWorld(w genWorld) (realLog, naiveLog []string, equal bool) {
	s := NewSession()
	n := newNaiveSim()
	for _, d := range w.modules {
		e1 := s.AddModule(d)
		e2 := n.add(d)
		realLog = append(realLog, fmt.Sprintf("ADD %s -> %s", d.Name, errClass(e1)))
		naiveLog = append(naiveLog, fmt.Sprintf("ADD %s -> %s", d.Name, errClass(e2)))
		if errClass(e1) != errClass(e2) {
			return
		}
	}
	for _, root := range w.roots {
		r1, err1 := s.Evaluate(root)
		r2 := n.evaluate(root)
		k1, k2 := "", ""
		if err1 != nil {
			k1 = "REJECT " + errClass(err1)
		} else {
			k1 = "EVAL " + resultKey(r1)
		}
		if r2.rej != nil {
			k2 = "REJECT " + errClass(r2.rej)
		} else {
			k2 = "EVAL " + naiveResultKey(r2)
		}
		realLog = append(realLog, "EVAL "+root+" -> "+k1)
		naiveLog = append(naiveLog, "EVAL "+root+" -> "+k2)
		if k1 != k2 {
			return
		}
		// 不变量：没有模块处于求值中；次序中无重复；出错模块不在次序。
		for name, m := range s.modules {
			if m.status == StatusEvaluating {
				realLog = append(realLog, "INVARIANT evaluating left: "+name)
				return
			}
		}
	}
	s1, s2 := snapshotSession(s), snapshotNaive(n)
	equal = reflect.DeepEqual(s1.status, s2.status) &&
		reflect.DeepEqual(s1.lets, s2.lets) &&
		reflect.DeepEqual(s1.order, s2.order)
	if !equal {
		realLog = append(realLog, "SNAP REAL  "+fmt.Sprintf("%+v %+v %v", s1.status, s1.lets, s1.order))
		naiveLog = append(naiveLog, "SNAP NAIVE "+fmt.Sprintf("%+v %+v %v", s2.status, s2.lets, s2.order))
	}
	return
}

func TestRandomDifferential2000(t *testing.T) {
	rng := rand.New(rand.NewSource(20261002))
	var logFile *os.File
	if *diffLog != "" {
		f, err := os.Create(*diffLog)
		if err != nil {
			t.Fatalf("open difflog: %v", err)
		}
		defer f.Close()
		logFile = f
	}
	failures := 0
	for trial := 0; trial < 2000; trial++ {
		w := generateWorld(rng)
		realLog, naiveLog, equal := runWorld(w)
		if logFile != nil {
			fmt.Fprintf(logFile, "=== trial %d ===\n", trial)
			fmt.Fprintf(logFile, "INPUT roots=%v\n", w.roots)
			for i := range realLog {
				fmt.Fprintf(logFile, "  real : %s\n", realLog[i])
				fmt.Fprintf(logFile, "  naive: %s\n", naiveLog[i])
			}
			fmt.Fprintf(logFile, "VERDICT equal=%v\n", equal)
		}
		if !equal {
			failures++
			t.Errorf("trial %d mismatch\n  real : %s\n  naive: %s",
				trial, strings.Join(realLog, "\n  real : "), strings.Join(naiveLog, "\n  naive: "))
			if failures >= 5 {
				return
			}
		}
	}
}

// 并发：多个 goroutine 串行等价地交错登记与求值，不应崩溃或留下中间状态。
func TestConcurrentSessions(t *testing.T) {
	s := NewSession()
	mustAdd(t, s, ModuleDef{Name: "B", Exports: []Export{{"g", KindFunction}}})
	mustAdd(t, s, ModuleDef{
		Name:    "A",
		Imports: []ImportStmt{{Source: "B", Bindings: []string{"g"}}},
		Exports: []Export{{"a", KindLet}},
		Body:    []Step{Read("B", "g"), Init("a")},
	})
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, _ = s.Evaluate("A")
				_ = s.Order()
				_, _ = s.Status("B")
			}
		}()
	}
	wg.Wait()
	for name, m := range s.modules {
		if m.status == StatusEvaluating {
			t.Fatalf("%s left evaluating", name)
		}
	}
	if got := statusOf(t, s, "A"); got.Status != StatusEvaluated {
		t.Fatalf("A=%v", got.Status)
	}
}
