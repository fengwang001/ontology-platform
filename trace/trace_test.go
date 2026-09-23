package trace

import (
	"math/rand"
	"testing"

	"ontology/expand"
	"ontology/merge"
	"ontology/source"
)

// buildReport 构造四层来源（greeting 四层都有，only 只在默认值层），
// 合并、展开后生成溯源报告。构造顺序由 order 决定。
func buildReport(order []int) (*Report, map[string]string) {
	type namedEntries struct {
		name    string
		entries []source.Entry
	}
	layers := map[string][]source.Entry{}
	layers["default"] = []source.Entry{
		{Key: "greeting", Value: "hi ${name}", Layer: source.LayerDefault},
		{Key: "name", Value: "d", Layer: source.LayerDefault},
		{Key: "only", Value: "o", Layer: source.LayerDefault},
	}
	layers["file"] = []source.Entry{
		{Key: "greeting", Value: "hello ${name}", Layer: source.LayerFile},
		{Key: "name", Value: "file", Layer: source.LayerFile},
	}
	layers["env"] = []source.Entry{
		{Key: "greeting", Value: "hey ${name}", Layer: source.LayerEnv},
		{Key: "name", Value: "env", Layer: source.LayerEnv},
	}
	layers["args"] = []source.Entry{
		{Key: "greeting", Value: "yo ${name}", Layer: source.LayerArgs},
	}
	names := []string{"default", "file", "env", "args"}
	built := make([]namedEntries, 0, 4)
	for _, i := range order {
		built = append(built, namedEntries{names[i], layers[names[i]]})
	}
	byName := map[string][]source.Entry{}
	for _, b := range built {
		byName[b.name] = b.entries
	}
	var m merge.Merger
	records := m.Merge(byName["default"], byName["file"], byName["env"], byName["args"])
	var e expand.Expander
	expanded, err := e.ExpandAll(merge.Values(records))
	if err != nil {
		panic(err)
	}
	return Build(records, expanded), expanded
}

func TestTraceFourLayers(t *testing.T) {
	report, _ := buildReport([]int{0, 1, 2, 3})
	kt, ok := report.Find("greeting")
	if !ok {
		t.Fatal("greeting trace missing")
	}
	if kt.Source != source.LayerArgs || kt.Raw != "yo ${name}" || kt.Expanded != "yo env" {
		t.Fatalf("got source=%v raw=%q expanded=%q", kt.Source, kt.Raw, kt.Expanded)
	}
	want := []struct {
		layer source.Layer
		value string
	}{
		{source.LayerDefault, "hi ${name}"},
		{source.LayerFile, "hello ${name}"},
		{source.LayerEnv, "hey ${name}"},
		{source.LayerArgs, "yo ${name}"},
	}
	if len(kt.History) != len(want) {
		t.Fatalf("history = %+v, want %d layers", kt.History, len(want))
	}
	for i, w := range want {
		if kt.History[i].Layer != w.layer || kt.History[i].Value != w.value {
			t.Fatalf("history[%d] = %+v, want %v %q", i, kt.History[i], w.layer, w.value)
		}
	}
}

func TestTraceSingleLayer(t *testing.T) {
	report, _ := buildReport([]int{0, 1, 2, 3})
	kt, ok := report.Find("only")
	if !ok {
		t.Fatal("only trace missing")
	}
	if len(kt.History) != 1 || kt.History[0].Layer != source.LayerDefault {
		t.Fatalf("history = %+v, want single default layer", kt.History)
	}
	if kt.Raw != "o" || kt.Expanded != "o" || kt.Source != source.LayerDefault {
		t.Fatalf("got %+v", kt)
	}
}

// TestTraceRawAndExpanded 含引用的键必须同时报原始值与展开值。
func TestTraceRawAndExpanded(t *testing.T) {
	var m merge.Merger
	records := m.Merge(
		[]source.Entry{{Key: "greeting", Value: "hello ${name}", Layer: source.LayerFile}},
		[]source.Entry{{Key: "name", Value: "env", Layer: source.LayerEnv}},
	)
	var e expand.Expander
	expanded, err := e.ExpandAll(merge.Values(records))
	if err != nil {
		t.Fatal(err)
	}
	kt, _ := Build(records, expanded).Find("greeting")
	if kt.Raw != "hello ${name}" || kt.Expanded != "hello env" {
		t.Fatalf("raw=%q expanded=%q", kt.Raw, kt.Expanded)
	}
}

// TestDeterministic 打乱四层来源构造顺序 20 次，报告逐字节一致。
func TestDeterministic(t *testing.T) {
	base, _ := buildReport([]int{0, 1, 2, 3})
	want := base.String()
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 20; i++ {
		order := []int{0, 1, 2, 3}
		rng.Shuffle(len(order), func(a, b int) { order[a], order[b] = order[b], order[a] })
		report, _ := buildReport(order)
		if got := report.String(); got != want {
			t.Fatalf("shuffle %d (%v) differs:\n%s\nwant:\n%s", i, order, got, want)
		}
	}
}
