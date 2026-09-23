package bucket

import (
	"slices"
	"testing"

	"ontology/hyper"
)

func TestTables(t *testing.T) {
	tests := []struct {
		name   string
		adds   []add
		lookups []lookup
		buckets []int
	}{
		{
			name:    "multi-table",
			adds:    []add{{0, 1, 10}, {0, 1, 11}, {0, 2, 12}, {1, 1, 10}},
			lookups: []lookup{{0, 1, []ID{10, 11}}, {0, 2, []ID{12}}, {1, 1, []ID{10}}, {1, 9, nil}},
			buckets: []int{2, 1},
		},
		{
			name:    "empty",
			lookups: []lookup{{0, 0, nil}},
			buckets: []int{0},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := New(len(tt.buckets))
			for _, a := range tt.adds {
				ts.Add(a.table, hyper.Signature(a.sig), ID(a.id))
			}
			for _, lk := range tt.lookups {
				got := ts.Get(lk.table, hyper.Signature(lk.sig))
				if !slices.Equal([]ID(got), lk.want) {
					t.Fatalf("Get(t=%d,sig=%d)=%v want %v",
						lk.table, lk.sig, got, lk.want)
				}
			}
			for t0, want := range tt.buckets {
				if ts.BucketCount(t0) != want {
					t.Fatalf("BucketCount(%d)=%d want %d",
						t0, ts.BucketCount(t0), want)
				}
			}
		})
	}
}

type add struct{ table, sig, id int }
type lookup struct {
	table, sig int
	want       []ID
}

func TestSnapshotIsolation(t *testing.T) {
	ts := New(1)
	ts.Add(0, 5, 1)
	snap := ts.Snapshot()
	ts.Add(0, 5, 2)
	ts.Add(0, 6, 3)
	got := snap.Get(0, 5)
	if !slices.Equal([]ID(got), []ID{1}) {
		t.Fatalf("snapshot leaked later writes: %v", got)
	}
}

func TestEntries(t *testing.T) {
	ts := New(1)
	ts.Add(0, 1, 9)
	ts.Add(0, 1, 8)
	es := ts.Entries(0)
	if len(es) != 1 || es[0].Sig != 1 ||
		!slices.Equal(es[0].IDs, []ID{9, 8}) {
		t.Fatalf("Entries=%+v", es)
	}
}
