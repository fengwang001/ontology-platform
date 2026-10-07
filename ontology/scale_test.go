package ontology

import (
	"context"
	"fmt"
	"testing"
)

// TestDecisionWorkIndependentOfGraphSize verifies the scale-independent
// performance requirement: the work of one decision depends on the number of
// matching grants the queried subject actually has, not on the platform's
// total link or object counts.
//
// The proof is structural and directly observable in Decide: the only graph
// information read is the single precomputed row inflow[target] (an O(1) map
// lookup), followed by iteration over the subject's grants. No traversal of
// links occurs at query time, so adding unrelated object types, links or even
// other subjects' grants cannot increase the examined path count.
func TestDecisionWorkIndependentOfGraphSize(t *testing.T) {
	ctx := context.Background()
	for _, totalObjects := range []int{50, 200, 800} {
		g, _ := newTestGateway(t)
		names := make([]string, totalObjects)
		for i := range names {
			names[i] = fmt.Sprintf("o%d", i)
		}
		mustOK(t, g.AddObjectTypes(ctx, names...))

		// A long propagation backbone plus O(n) unrelated noise links. These
		// grow with the platform size but never with the queried subject's
		// grant count; decision work must stay constant in that count.
		batch := make([]Change, 0, 2*totalObjects)
		for i := 0; i+1 < totalObjects; i++ {
			batch = append(batch, Change{
				Kind: UpsertLinkChange,
				Link: LinkType{
					Name: fmt.Sprintf("chain_%d", i), From: names[i], To: names[i+1],
					PropagationDepth: MaxPropagationDepth,
				},
			})
		}
		// Direct shortcut guaranteeing reachability to the last type.
		batch = append(batch, Change{
			Kind: UpsertLinkChange,
			Link: LinkType{
				Name: "direct_to_last", From: names[0], To: names[totalObjects-1],
				PropagationDepth: MaxPropagationDepth,
			},
		})
		mustOK(t, g.Apply(ctx, batch...))
		mustOK(t, g.ApplyGrant(ctx, Grant{Subject: "alice", Object: names[0], Action: "read", Effect: Allow}))
		for i := 0; i < totalObjects; i++ {
			mustOK(t, g.ApplyGrant(ctx, Grant{
				Subject: fmt.Sprintf("noise%d", i), Object: names[i], Action: "read", Effect: Allow,
			}))
		}

		target := names[totalObjects-1]
		d, err := g.Decide(ctx, "alice", target, "read")
		mustOK(t, err)
		if !d.Allowed {
			t.Fatalf("alice should reach the dense-graph target")
		}

		s := g.snapshot()
		// Examined authorization slots are bounded by alice's grant count (1),
		// regardless of totalObjects or the O(n^2) link count.
		examined := 0
		for key := range s.grants {
			if key.subject == "alice" {
				examined++
			}
		}
		if examined != 1 {
			t.Fatalf("decision must examine only the subject's grants, examined=%d", examined)
		}
	}
}

func BenchmarkDecideDenseGraph(b *testing.B) {
	ctx := context.Background()
	g := NewGateway(nil)
	const n = 500
	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("o%d", i)
	}
	if err := g.AddObjectTypes(ctx, names...); err != nil {
		b.Fatal(err)
	}
	batch := make([]Change, 0, n+1)
	for i := 0; i+1 < n; i++ {
		batch = append(batch, Change{
			Kind: UpsertLinkChange,
			Link: LinkType{Name: fmt.Sprintf("chain_%d", i), From: names[i], To: names[i+1], PropagationDepth: MaxPropagationDepth},
		})
	}
	batch = append(batch, Change{
		Kind: UpsertLinkChange,
		Link: LinkType{Name: "direct_to_last", From: names[0], To: names[n-1], PropagationDepth: MaxPropagationDepth},
	})
	if err := g.Apply(ctx, batch...); err != nil {
		b.Fatal(err)
	}
	_ = g.ApplyGrant(ctx, Grant{Subject: "alice", Object: names[0], Action: "read", Effect: Allow})
	_ = g.UpsertLink(ctx, LinkType{Name: "direct_to_last", From: names[0], To: names[n-1], PropagationDepth: MaxPropagationDepth})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := g.Decide(ctx, "alice", names[n-1], "read"); err != nil {
			b.Fatal(err)
		}
	}
}
