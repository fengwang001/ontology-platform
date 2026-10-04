package plan

import (
	"errors"
	"testing"
)

func TestEval(t *testing.T) {
	leaf := func(c int, parts ...int) *Leaf { return &Leaf{Cost: c, Parts: parts} }
	tests := []struct {
		name    string
		root    Node
		want    int64
		parts   map[int]struct{}
		nodes   int
		wantErr error
	}{
		{
			name:  "leaf",
			root:  leaf(100, 1),
			want:  100,
			parts: map[int]struct{}{1: {}},
			nodes: 1,
		},
		{
			name:  "seq sums children",
			root:  &Seq{[]Node{leaf(10, 1), leaf(20, 2), leaf(30, 3)}},
			want:  60,
			parts: map[int]struct{}{1: {}, 2: {}, 3: {}},
			nodes: 4,
		},
		{
			name:  "par takes max with disjoint parts",
			root:  &Par{[]Node{leaf(80, 2), leaf(120, 3)}},
			want:  120,
			parts: map[int]struct{}{2: {}, 3: {}},
			nodes: 3,
		},
		{
			name:    "par overlapping parts rejected",
			root:    &Par{[]Node{leaf(80, 2), leaf(120, 2)}},
			nodes:   3,
			wantErr: ErrNotDisjoint,
		},
		{
			name:    "nested par overlap detected",
			root:    &Par{[]Node{&Par{[]Node{leaf(1, 1), leaf(2, 2)}}, leaf(3, 2)}},
			nodes:   5,
			wantErr: ErrNotDisjoint,
		},
		{
			name:  "sample ceils per node - three separate samples",
			root:  &Seq{[]Node{&Sample{1, 3, leaf(1, 1)}, &Sample{1, 3, leaf(1, 2)}, &Sample{1, 3, leaf(1, 3)}}},
			want:  3,
			parts: map[int]struct{}{1: {}, 2: {}, 3: {}},
			nodes: 7,
		},
		{
			name:  "sample ceils once at its node - seq inside sample",
			root:  &Sample{1, 3, &Seq{[]Node{leaf(1, 1), leaf(1, 2), leaf(1, 3)}}},
			want:  1,
			parts: map[int]struct{}{1: {}, 2: {}, 3: {}},
			nodes: 5,
		},
		{
			name:  "spec example plan cost 254",
			root:  &Seq{[]Node{leaf(100, 1), &Par{[]Node{leaf(80, 2), leaf(120, 3)}}, &Sample{1, 3, leaf(100, 4)}}},
			want:  254,
			parts: map[int]struct{}{1: {}, 2: {}, 3: {}, 4: {}},
			nodes: 7,
		},
		{
			name:  "sample num equals den keeps cost",
			root:  &Sample{10, 10, leaf(7, 1)},
			want:  7,
			parts: map[int]struct{}{1: {}},
			nodes: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Eval(tt.root)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Eval err = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				return
			}
			if got.Cost != tt.want {
				t.Errorf("cost = %d, want %d", got.Cost, tt.want)
			}
			if got.Nodes != tt.nodes {
				t.Errorf("nodes = %d, want %d", got.Nodes, tt.nodes)
			}
			if len(got.Parts) != len(tt.parts) {
				t.Fatalf("parts = %v, want %v", got.Parts, tt.parts)
			}
			for p := range tt.parts {
				if _, ok := got.Parts[p]; !ok {
					t.Errorf("missing part %d in %v", p, got.Parts)
				}
			}
		})
	}
}

func TestEvalInvalid(t *testing.T) {
	leaf := func(c int, parts ...int) *Leaf { return &Leaf{Cost: c, Parts: parts} }
	tests := []struct {
		name string
		root Node
	}{
		{"nil root", nil},
		{"leaf zero cost", leaf(0, 1)},
		{"leaf no parts", leaf(10)},
		{"leaf too many parts", leaf(1, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17)},
		{"leaf duplicate parts", leaf(1, 1, 1)},
		{"seq no children", &Seq{nil}},
		{"seq too many children", &Seq{children(17, leaf(1, 1))}},
		{"par no children", &Par{nil}},
		{"sample num zero", &Sample{0, 3, leaf(3, 1)}},
		{"sample num greater den", &Sample{4, 3, leaf(3, 1)}},
		{"sample den too large", &Sample{1, 1_000_001, leaf(3, 1)}},
		{"sample nil child", &Sample{1, 3, nil}},
		{"root cost over 1e12", leaf(1_000_000_000_001, 1)},
		{"too many nodes 257", chainSeq(257, leaf(1, 1))},
		{"depth 9", nestSample(8, leaf(1, 1))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Eval(tt.root); !errors.Is(err, ErrInvalidPlan) {
				t.Fatalf("err = %v, want ErrInvalidPlan", err)
			}
		})
	}
}

func children(n int, c Node) []Node {
	cs := make([]Node, n)
	for i := range cs {
		cs[i] = c
	}
	return cs
}

func leaf(c int, parts ...int) *Leaf { return &Leaf{Cost: c, Parts: parts} }

// chainSeq builds n nodes total using right-leaning Seqs sharing distinct parts.
func chainSeq(n int, c Node) Node {
	if n == 1 {
		return c
	}
	return &Seq{[]Node{leaf(1, 100+n), chainSeq(n-1, c)}}
}

// nestSample wraps leaf in k Samples, giving depth k+1.
func nestSample(k int, c Node) Node {
	if k == 0 {
		return c
	}
	return &Sample{1, 2, nestSample(k-1, c)}
}
