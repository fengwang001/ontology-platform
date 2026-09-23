package merge

import (
	"fmt"
	"testing"

	"ontology/source"
)

func entries(layer source.Layer, kvs ...string) []source.Entry {
	out := make([]source.Entry, 0, len(kvs)/2)
	for i := 0; i+1 < len(kvs); i += 2 {
		out = append(out, source.Entry{Key: kvs[i], Value: kvs[i+1], Layer: layer})
	}
	return out
}

func TestMerge(t *testing.T) {
	cases := []struct {
		name   string
		layers [][]source.Entry
		key    string
		want   Merged
	}{
		{"priority order", [][]source.Entry{
			entries(source.Default, "k", "d"),
			entries(source.File, "k", "f"),
			entries(source.Env, "k", "e"),
			entries(source.CLI, "k", "c"),
		}, "k", Merged{Value: "c", Layer: source.CLI, Overridden: []Override{
			{source.Default, "d"}, {source.File, "f"}, {source.Env, "e"},
		}}},
		{"default only", [][]source.Entry{
			entries(source.Default, "k", "d"),
		}, "k", Merged{Value: "d", Layer: source.Default}},
		{"empty value kept", [][]source.Entry{
			entries(source.Default, "k", "d"),
			entries(source.Env, "k", ""),
		}, "k", Merged{Value: "", Layer: source.Env, Overridden: []Override{
			{source.Default, "d"},
		}}},
		{"same layer last wins", [][]source.Entry{
			entries(source.File, "k", "a", "k", "b"),
		}, "k", Merged{Value: "b", Layer: source.File, Overridden: []Override{
			{source.File, "a"},
		}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Merge(tc.layers...).Entries[tc.key]
			if got.Value != tc.want.Value || got.Layer != tc.want.Layer {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
			if len(got.Overridden) != len(tc.want.Overridden) {
				t.Fatalf("overridden %+v, want %+v", got.Overridden, tc.want.Overridden)
			}
			for i, o := range got.Overridden {
				if o != tc.want.Overridden[i] {
					t.Fatalf("overridden[%d] = %+v, want %+v", i, o, tc.want.Overridden[i])
				}
			}
		})
	}
}

func TestMergeEmpty(t *testing.T) {
	if n := len(Merge().Entries); n != 0 {
		t.Fatalf("empty merge has %d entries", n)
	}
}

func TestMergeLookupBound(t *testing.T) {
	const perLayer = 10000
	layers := make([][]source.Entry, 4)
	for l := range layers {
		for i := 0; i < perLayer; i++ {
			layers[l] = append(layers[l], source.Entry{
				Key:   fmt.Sprintf("k%d", i),
				Value: "v",
				Layer: source.Layer(l),
			})
		}
	}
	res := Merge(layers...)
	total := len(res.Entries)
	if lookups > 4*total {
		t.Fatalf("lookups %d exceed bound 4*%d", lookups, total)
	}
	if lookups != 4*perLayer {
		t.Fatalf("lookups = %d, want exactly %d (one read per entry)",
			lookups, 4*perLayer)
	}
}
