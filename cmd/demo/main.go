// Command demo 演示带依赖的配置解析与环境覆盖器的全部判定。
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"strings"

	"ontology/expand"
	"ontology/merge"
	"ontology/source"
	"ontology/trace"
	"ontology/validate"
)

var failures int

func check(ok bool, label string) {
	if ok {
		fmt.Printf("OK   %s\n", label)
		return
	}
	failures++
	fmt.Printf("FAIL %s\n", label)
}

// traceReport 按给定构造顺序建四层来源，再按层标记归位合并，返回报告字符串。
func traceReport(order []int) string {
	layers := [][]source.Entry{
		{{Key: "greeting", Value: "hi ${name}", Layer: source.LayerDefault}, {Key: "name", Value: "d", Layer: source.LayerDefault}},
		{{Key: "greeting", Value: "hello ${name}", Layer: source.LayerFile}, {Key: "name", Value: "file", Layer: source.LayerFile}},
		{{Key: "greeting", Value: "hey ${name}", Layer: source.LayerEnv}, {Key: "name", Value: "env", Layer: source.LayerEnv}},
		{{Key: "greeting", Value: "hello ${name}", Layer: source.LayerArgs}},
	}
	ordered := make([][]source.Entry, 4)
	for _, idx := range order { // 构造顺序打乱，归位仍按层优先级
		ordered[idx] = layers[idx]
	}
	var m merge.Merger
	records := m.Merge(ordered[0], ordered[1], ordered[2], ordered[3])
	var e expand.Expander
	expanded, err := e.ExpandAll(merge.Values(records))
	if err != nil {
		panic(err)
	}
	return trace.Build(records, expanded).String()
}

func main() {
	file, _ := source.ParseFile([]byte("greeting = hello ${name}\nname = file\n"))
	env, _ := source.Env("", []string{"NAME=env"})
	var m merge.Merger
	var ex expand.Expander
	out, err := ex.ExpandAll(merge.Values(m.Merge(file, env)))
	check(err == nil && out["greeting"] == "hello env", "merge-then-expand: greeting = 'hello env'")

	var cyc expand.Expander
	_, err = cyc.ExpandAll(map[string]string{"a": "${b}", "b": "${a}"})
	check(errors.Is(err, expand.ErrCycle) && strings.Contains(err.Error(), "a -> b -> a"),
		"cycle detected, path closed: a -> b -> a")

	var esc expand.Expander
	escOut, _ := esc.ExpandAll(map[string]string{"a": "x", "b": "$${a}"})
	check(escOut["b"] == "${a}", "escape: $${a} stays literal ${a}")

	var unc expand.Expander
	_, err = unc.ExpandAll(map[string]string{"a": "xy${b"})
	check(errors.Is(err, expand.ErrUnclosedRef) && strings.Contains(err.Error(), "offset 2"),
		"unclosed ${ reports byte offset 2")

	report := traceReport([]int{0, 1, 2, 3})
	check(strings.Contains(report, `history=[default:"hi ${name}" file:"hello ${name}" env:"hey ${name}" args:"hello ${name}"]`),
		"four-layer trace order: default < file < env < args")
	check(strings.Contains(report, `raw="hello ${name}"`) && strings.Contains(report, `expanded="hello env"`),
		"report holds both raw 'hello ${name}' and expanded 'hello env'")

	values := map[string]string{"k0": "x"}
	for i := 1; i <= 20; i++ {
		values[fmt.Sprintf("k%d", i)] = fmt.Sprintf("${k%d}${k%d}", i-1, i-1)
	}
	var chain expand.Expander
	if _, err := chain.ExpandAll(values); err != nil {
		panic(err)
	}
	bound := 4 * len(values) * 2 // 4 * 键数 * 平均引用数(≈2)
	check(chain.Replacements() == 40 && chain.Replacements() <= bound,
		fmt.Sprintf("k20 replacements %d <= bound %d (naive 2^20)", chain.Replacements(), bound))

	terr := validate.Check([]validate.Rule{{Key: "port", Type: validate.Int}},
		map[string]string{"port": "abc"}, map[string]source.Layer{"port": source.LayerEnv})
	ok := errors.Is(terr, validate.ErrTypeMismatch)
	for _, part := range []string{`"port"`, "int", `"abc"`, "env"} {
		ok = ok && strings.Contains(terr.Error(), part)
	}
	check(ok, "type error carries key, expected type, value, layer")

	merr := validate.Check([]validate.Rule{{Key: "a.req", Required: true}, {Key: "b.req", Required: true}},
		map[string]string{}, map[string]source.Layer{})
	check(errors.Is(merr, validate.ErrMissingRequired) &&
		strings.Contains(merr.Error(), "a.req") && strings.Contains(merr.Error(), "b.req"),
		"all missing required keys reported at once")

	_, aerr := source.Env("", []string{"A_B_C=1", "A__B_C=2"})
	check(errors.Is(aerr, source.ErrEnvAmbiguous), "env name ambiguity A_B_C vs A__B_C detected")

	content := "greeting = hello ${name}\n"
	_, e1 := source.ParseFile([]byte(content[:5]))
	_, e2 := source.ParseFile([]byte(content[:10]))
	_, e3 := source.ParseFile([]byte(content[:15]))
	_, e4 := source.ParseFile([]byte(content))
	check(errors.Is(e1, source.ErrKeyIncomplete) && errors.Is(e2, source.ErrValueIncomplete) &&
		errors.Is(e3, source.ErrLineIncomplete) && e4 == nil,
		"truncation classes: key/value/line incomplete, full file parses")

	base := traceReport([]int{0, 1, 2, 3})
	rng := rand.New(rand.NewSource(1))
	same := true
	for i := 0; i < 20; i++ {
		order := []int{0, 1, 2, 3}
		rng.Shuffle(4, func(a, b int) { order[a], order[b] = order[b], order[a] })
		if traceReport(order) != base {
			same = false
		}
	}
	check(same, "shuffling source construction order 20x: identical output")

	total := 12
	fmt.Printf("TOTAL %d checks, %d failed\n", total, failures)
	if failures > 0 {
		os.Exit(1)
	}
}
