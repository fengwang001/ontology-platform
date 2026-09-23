package merge

import (
	"fmt"
	"testing"

	"ontology/source"
)

func layer(l source.Layer, kv ...string) []source.Entry {
	var entries []source.Entry
	for i := 0; i < len(kv); i += 2 {
		entries = append(entries, source.Entry{Key: kv[i], Value: kv[i+1], Layer: l})
	}
	return entries
}

func TestMergePriority(t *testing.T) {
	cases := []struct {
		name   string
		layers [][]source.Entry
		key    string
		want   string
		wantL  source.Layer
	}{
		{"args beats all", [][]source.Entry{
			layer(source.LayerDefault, "k", "d"), layer(source.LayerFile, "k", "f"),
			layer(source.LayerEnv, "k", "e"), layer(source.LayerArgs, "k", "a"),
		}, "k", "a", source.LayerArgs},
		{"env beats file", [][]source.Entry{
			layer(source.LayerFile, "k", "f"), layer(source.LayerEnv, "k", "e"),
		}, "k", "e", source.LayerEnv},
		{"file beats default", [][]source.Entry{
			layer(source.LayerDefault, "k", "d"), layer(source.LayerFile, "k", "f"),
		}, "k", "f", source.LayerFile},
		{"single layer", [][]source.Entry{
			layer(source.LayerDefault, "k", "d"),
		}, "k", "d", source.LayerDefault},
		{"same layer last wins", [][]source.Entry{
			layer(source.LayerFile, "k", "f1", "k", "f2"),
		}, "k", "f2", source.LayerFile},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var m Merger
			rec := m.Merge(c.layers...)[c.key]
			if rec == nil || rec.Value != c.want || rec.Layer != c.wantL {
				t.Fatalf("got %+v, want value %q layer %v", rec, c.want, c.wantL)
			}
		})
	}
}

func TestMergeHistoryOrder(t *testing.T) {
	var m Merger
	rec := m.Merge(
		layer(source.LayerDefault, "k", "d"),
		layer(source.LayerFile, "k", "f"),
		layer(source.LayerEnv, "k", "e"),
		layer(source.LayerArgs, "k", "a"),
	)["k"]
	want := []string{"d", "f", "e", "a"}
	if len(rec.History) != len(want) {
		t.Fatalf("history = %+v, want %d entries", rec.History, len(want))
	}
	for i, e := range rec.History {
		if e.Value != want[i] || e.Layer != source.Layer(i) {
			t.Fatalf("history[%d] = %+v, want value %q layer %d", i, e, want[i], i)
		}
	}
}

// TestMergeLookupBound 四层各 1 万键，查找次数不超过 4*总键数。
func TestMergeLookupBound(t *testing.T) {
	const perLayer = 10000
	layers := make([][]source.Entry, 4)
	for l := range layers {
		for i := 0; i < perLayer; i++ {
			key := fmt.Sprintf("key.%d", i)
			layers[l] = append(layers[l], source.Entry{Key: key, Value: "v", Layer: source.Layer(l)})
		}
	}
	var m Merger
	records := m.Merge(layers...)
	if bound := 4 * len(records); m.lookups > bound {
		t.Fatalf("lookups = %d, bound = %d", m.lookups, bound)
	}
	if m.lookups != 4*perLayer {
		t.Fatalf("lookups = %d, want exactly one per entry (%d)", m.lookups, 4*perLayer)
	}
}
