package searcher

import (
	"reflect"
	"testing"
)

func TestViewApplyGetList(t *testing.T) {
	tests := []struct {
		name  string
		ops   func(v *View)
		want  []Doc
		alive map[string]bool
	}{
		{
			name: "index then visible sorted by id bytes",
			ops: func(v *View) {
				v.ApplyIndex([]byte("b"), []byte("2"), 2)
				v.ApplyIndex([]byte("a"), []byte("1"), 1)
			},
			want: []Doc{
				{ID: []byte("a"), Body: []byte("1"), Seq: 1},
				{ID: []byte("b"), Body: []byte("2"), Seq: 2},
			},
			alive: map[string]bool{"a": true, "b": true},
		},
		{
			name: "delete removes from view",
			ops: func(v *View) {
				v.ApplyIndex([]byte("a"), []byte("1"), 1)
				v.ApplyDelete([]byte("a"))
			},
			want:  []Doc{},
			alive: map[string]bool{"a": false},
		},
		{
			name: "reindex overwrites body and seq",
			ops: func(v *View) {
				v.ApplyIndex([]byte("a"), []byte("1"), 1)
				v.ApplyIndex([]byte("a"), []byte("2"), 5)
			},
			want:  []Doc{{ID: []byte("a"), Body: []byte("2"), Seq: 5}},
			alive: map[string]bool{"a": true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := New()
			tt.ops(v)
			if got := v.List(); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("List() = %+v, want %+v", got, tt.want)
			}
			for id, want := range tt.alive {
				if _, ok := v.Get([]byte(id)); ok != want {
					t.Fatalf("Get(%q) alive = %v, want %v", id, ok, want)
				}
			}
		})
	}
}

func TestSnapshotIsolation(t *testing.T) {
	v := New()
	v.ApplyIndex([]byte("a"), []byte("1"), 1)
	snap := v.Snapshot()
	v.ApplyIndex([]byte("a"), []byte("2"), 2)
	v.ApplyDelete([]byte("a"))
	doc, ok := snap.Get([]byte("a"))
	if !ok || string(doc.Body) != "1" || doc.Seq != 1 {
		t.Fatalf("snapshot changed by later ops: %+v ok=%v", doc, ok)
	}
	if snap.Len() != 1 || v.Len() != 0 {
		t.Fatalf("Len: snap=%d v=%d", snap.Len(), v.Len())
	}
}
