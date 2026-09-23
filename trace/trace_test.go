package trace_test

import (
	"fmt"
	"math/rand"
	"testing"

	"ontology/expand"
	"ontology/merge"
	"ontology/source"
	"ontology/trace"
)

// builders construct each source independently so tests can shuffle the
// construction order while keeping merge priority fixed.
var builders = []func() ([]source.Entry, error){
	func() ([]source.Entry, error) {
		return source.FromPairs(source.Default, map[string]string{
			"greeting": "hi ${name}", "name": "def", "who": "d0", "only": "o",
		})
	},
	func() ([]source.Entry, error) {
		return source.ParseFile(source.File,
			[]byte("greeting = hello ${name}\nname = file\nwho = f0\n.\n"))
	},
	func() ([]source.Entry, error) {
		return source.FromEnv(source.Env, []string{"NAME=env", "WHO=e0"}, "")
	},
	func() ([]source.Entry, error) {
		return source.ParseArgs(source.CLI, []string{"--who=c0"})
	},
}

func build(t *testing.T, constructOrder []int) []trace.Record {
	t.Helper()
	layers := make([][]source.Entry, 4)
	for _, i := range constructOrder {
		entries, err := builders[i]()
		if err != nil {
			t.Fatal(err)
		}
		layers[i] = entries
	}
	res := merge.Merge(layers...)
	vals := map[string]string{}
	for k, m := range res.Entries {
		vals[k] = m.Value
	}
	out, err := expand.New(vals).ExpandAll()
	if err != nil {
		t.Fatal(err)
	}
	return trace.Build(res, out)
}

func find(recs []trace.Record, key string) trace.Record {
	for _, r := range recs {
		if r.Key == key {
			return r
		}
	}
	return trace.Record{}
}

func TestTrace(t *testing.T) {
	recs := build(t, []int{0, 1, 2, 3})
	cases := []struct {
		name     string
		key      string
		final    string
		layers   []source.Layer // low -> high priority
		expanded string
	}{
		{"four layers", "who", "c0",
			[]source.Layer{source.Default, source.File, source.Env, source.CLI}, "c0"},
		{"default only", "only", "o", []source.Layer{source.Default}, "o"},
		{"raw and expanded", "greeting", "hello ${name}",
			[]source.Layer{source.Default, source.File}, "hello env"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := find(recs, tc.key)
			if r.Final.Value != tc.final || r.Expanded != tc.expanded {
				t.Fatalf("got %+v", r)
			}
			got := make([]source.Layer, 0, len(tc.layers))
			for _, o := range r.Overridden {
				got = append(got, o.Layer)
			}
			got = append(got, r.Final.Layer)
			if fmt.Sprint(got) != fmt.Sprint(tc.layers) {
				t.Fatalf("layers low->high %v, want %v", got, tc.layers)
			}
		})
	}
}

func TestDeterministic(t *testing.T) {
	want := trace.Report(build(t, []int{0, 1, 2, 3}))
	for trial := 0; trial < 20; trial++ {
		got := trace.Report(build(t, rand.Perm(4)))
		if got != want {
			t.Fatalf("trial %d differs:\n%s", trial, got)
		}
	}
}
