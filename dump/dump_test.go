package dump_test

import (
	"errors"
	"testing"

	"ontology/dump"
	"ontology/tree"
)

func countNodes(n *tree.Node) int {
	c := 1
	for _, ch := range n.Children {
		c += countNodes(ch)
	}
	return c
}

func chainTree() *tree.Tree {
	tr := tree.New()
	for i := 0; i < 5; i++ {
		tr.Insert([]string{"A", "B", "C"}, false)
	}
	return tr
}

func TestRoundTrip(t *testing.T) {
	cases := []struct {
		name  string
		build func() *tree.Tree
	}{
		{"empty tree", tree.New},
		{"single chain", chainTree},
		{"branching with truncation", func() *tree.Tree {
			tr := tree.New()
			tr.Insert([]string{"a", "b", "c"}, false)
			tr.Insert([]string{"a", "b", "d"}, false)
			tr.Insert([]string{"a", "b"}, true)
			tr.Insert([]string{"e"}, false)
			return tr
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := tc.build()
			got, err := dump.Unmarshal(dump.Marshal(tr))
			if err != nil {
				t.Fatal(err)
			}
			if got.Samples != tr.Samples || tree.SumSelf(got.Root) != tr.Samples {
				t.Fatalf("samples=%d self-sum=%d want %d", got.Samples, tree.SumSelf(got.Root), tr.Samples)
			}
			if countNodes(got.Root) != countNodes(tr.Root) {
				t.Fatalf("node count=%d want %d", countNodes(got.Root), countNodes(tr.Root))
			}
			if tree.SumTotal(got.Root) != tree.SumTotal(tr.Root) {
				t.Fatal("total-sum mismatch after round trip")
			}
		})
	}
}

func TestTruncation(t *testing.T) {
	data := dump.Marshal(chainTree()) // 4 节点：root(23B) + A/B/C(各 24B)，共 127B
	recEnds := []int{dump.HeaderSize + 23, dump.HeaderSize + 47, dump.HeaderSize + 71, dump.HeaderSize + 95}
	fileEnd := recEnds[3] + 4
	if len(data) != fileEnd {
		t.Fatalf("file len=%d want %d", len(data), fileEnd)
	}
	for n := 1; n < len(data); n++ {
		rec, _, err := dump.Recover(data[:n])
		var wantErr error
		wantNodes := 1 // 根节点恒存在
		switch {
		case n < dump.HeaderSize:
			wantErr = dump.ErrHeaderIncomplete
		case n < recEnds[3]:
			wantErr = dump.ErrRecordIncomplete
			for _, e := range recEnds[1:] {
				if n >= e {
					wantNodes++
				}
			}
		default:
			wantErr = dump.ErrCRCMismatch
			wantNodes = 4
		}
		if !errors.Is(err, wantErr) {
			t.Fatalf("n=%d: err=%v want %v", n, err, wantErr)
		}
		if got := countNodes(rec.Root); got != wantNodes {
			t.Fatalf("n=%d: recovered %d nodes want %d", n, got, wantNodes)
		}
		if tree.SumSelf(rec.Root) != rec.Samples {
			t.Fatalf("n=%d: recovered tree inconsistent", n)
		}
		var check func(nd *tree.Node)
		check = func(nd *tree.Node) {
			if nd.Total < nd.Self {
				t.Fatalf("n=%d: total<self at %q", n, nd.Frame)
			}
			for _, c := range nd.Children {
				check(c)
			}
		}
		check(rec.Root)
	}
}

func TestErrorClassification(t *testing.T) {
	data := dump.Marshal(chainTree())
	corrupt := append([]byte(nil), data...)
	corrupt[len(corrupt)-1] ^= 0xFF
	cases := []struct {
		name string
		data []byte
		want error
	}{
		{"header incomplete", data[:10], dump.ErrHeaderIncomplete},
		{"record incomplete", data[:dump.HeaderSize+30], dump.ErrRecordIncomplete},
		{"crc truncated", data[:len(data)-1], dump.ErrCRCMismatch},
		{"crc corrupted", corrupt, dump.ErrCRCMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := dump.Unmarshal(tc.data); !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want %v", err, tc.want)
			}
			for _, other := range []error{dump.ErrHeaderIncomplete, dump.ErrRecordIncomplete, dump.ErrCRCMismatch} {
				if other != tc.want {
					if _, err := dump.Unmarshal(tc.data); errors.Is(err, other) {
						t.Fatalf("err unexpectedly matches %v", other)
					}
				}
			}
		})
	}
}
