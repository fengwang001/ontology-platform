package ontology

import (
	"fmt"
	"math/rand"
	"testing"
)

// TestRandomDifferential generates many random acyclic propagation graphs
// with random per-link depths, overrides and grant sets and compares every
// (subject, target, action) answer of the indexed gateway against the
// independent naive DFS reference model, including denial reasons.
func TestRandomDifferential(t *testing.T) {
	const iterations = 200
	for seed := int64(0); seed < iterations; seed++ {
		rng := rand.New(rand.NewSource(seed))
		n := 2 + rng.Intn(6)
		nodes := make([]string, n)
		for i := range nodes {
			nodes[i] = fmt.Sprintf("n%d", i)
		}

		g, _ := newTestGateway(t)
		ctx := bg()
		mustOK(t, g.AddObjectTypes(ctx, nodes...))

		ref := newReferenceModel(MaxPropagationDepth)
		for _, node := range nodes {
			ref.objects[node] = true
		}
		linkID := 0
		addLink := func(link LinkType) {
			mustOK(t, g.UpsertLink(ctx, link))
			ref.links[link.Name] = link
		}

		// Edges always point forward in the topological ordering, guaranteeing
		// an acyclic propagation subgraph even for depth > 0 links.
		for i := 0; i < n; i++ {
			for j := i + 1; j < n; j++ {
				if rng.Intn(2) == 0 {
					continue
				}
				depth := rng.Intn(4) // 0..3, including non-propagating links
				name := fmt.Sprintf("l%d", linkID)
				linkID++
				link := LinkType{Name: name, From: nodes[i], To: nodes[j], PropagationDepth: depth}
				addLink(link)
			}
		}

		// Occasionally add an extra parallel edge to exercise path unions.
		if n >= 2 && rng.Intn(2) == 0 {
			depth := rng.Intn(4)
			link := LinkType{Name: fmt.Sprintf("lp%d", linkID), From: nodes[0], To: nodes[n-1], PropagationDepth: depth}
			linkID++
			addLink(link)
		}

		for _, node := range nodes {
			switch rng.Intn(4) {
			case 1:
				mustOK(t, g.SetOverride(ctx, node, ReplaceOverride))
				ref.overrides[node] = ReplaceOverride
			case 2:
				mustOK(t, g.SetOverride(ctx, node, BlockOverride))
				ref.overrides[node] = BlockOverride
			}
		}

		subjects := []string{"s1", "s2"}
		actions := []string{"read", "write"}
		grantCount := rng.Intn(n*2 + 1)
		for k := 0; k < grantCount; k++ {
			grant := Grant{
				Subject: subjects[rng.Intn(len(subjects))],
				Object:  nodes[rng.Intn(n)],
				Action:  actions[rng.Intn(len(actions))],
				Effect:  Allow,
			}
			if rng.Intn(4) == 0 {
				grant.Effect = Deny
			}
			mustOK(t, g.ApplyGrant(ctx, grant))
			ref.grants[grantKey{subject: grant.Subject, object: grant.Object, action: grant.Action}] = grant.Effect
		}

		for _, subject := range subjects {
			for _, node := range nodes {
				for _, action := range actions {
					got, err := g.Decide(ctx, subject, node, action)
					if err != nil {
						t.Fatalf("seed %d: unexpected decide error: %v", seed, err)
					}
					want := ref.decide(subject, node, action)
					if got.Allowed != want.allowed {
						t.Fatalf("seed %d mismatch subject=%s node=%s action=%s: got allowed=%v(%s) want allowed=%v(%s)",
							seed, subject, node, action, got.Allowed, got.DenyReason, want.allowed, want.denyReason)
					}
					if !got.Allowed && got.DenyReason != want.denyReason {
						t.Fatalf("seed %d denial-reason mismatch subject=%s node=%s action=%s: got %s want %s",
							seed, subject, node, action, got.DenyReason, want.denyReason)
					}
				}
			}
		}
	}
}
