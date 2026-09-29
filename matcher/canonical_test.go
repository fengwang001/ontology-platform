package matcher

import "testing"

// 对称菱形 a,b 同为 T，x 为 U，边类型相同：a 与 b 在模式自同构下对称。
func diamondPattern() *Pattern {
	return &Pattern{
		Nodes: []NodeVar{
			{Name: "a", Type: "T"},
			{Name: "b", Type: "T"},
			{Name: "x", Type: "U"},
		},
		Edges: []EdgePat{
			{Type: "knows", Source: "a", Target: "x"},
			{Type: "knows", Source: "b", Target: "x"},
		},
	}
}

func TestCanonicalKey_AutomorphicEmbeddingsCollide(t *testing.T) {
	p := diamondPattern()
	emb1 := map[string]string{"a": "o1", "b": "o2", "x": "ox"}
	emb2 := map[string]string{"a": "o2", "b": "o1", "x": "ox"}
	key1 := canonicalKey(p, emb1)
	key2 := canonicalKey(p, emb2)
	if key1 != key2 {
		t.Fatalf("automorphic embeddings must share canonical key:\n%q\n%q", key1, key2)
	}
}

func TestCanonicalKey_DistinctObjectSetsDiffer(t *testing.T) {
	p := diamondPattern()
	emb1 := map[string]string{"a": "o1", "b": "o2", "x": "ox"}
	emb2 := map[string]string{"a": "o1", "b": "o3", "x": "ox"}
	if canonicalKey(p, emb1) == canonicalKey(p, emb2) {
		t.Fatalf("embeddings over different object sets must have different keys")
	}
}

func TestCanonicalKey_DirectedEdgesBreakSymmetry(t *testing.T) {
	// 有向边使两个 T 变量不对称：a -> x 与 b -> x 本身仍对称，
	// 再加一条 x -> a 后 a 与 b 不再可互换。
	p := diamondPattern()
	p.Edges = append(p.Edges, EdgePat{Type: "knows", Source: "x", Target: "a"})
	emb1 := map[string]string{"a": "o1", "b": "o2", "x": "ox"}
	emb2 := map[string]string{"a": "o2", "b": "o1", "x": "ox"}
	if canonicalKey(p, emb1) == canonicalKey(p, emb2) {
		t.Fatalf("directed edge must break symmetry between a and b")
	}
}

func TestCanonicalKey_ConstraintSignatureBreaksSymmetry(t *testing.T) {
	p := diamondPattern()
	p.Nodes[0].Constraints = []Constraint{{Attr: "age", Op: OpGt, Value: 10}}
	emb1 := map[string]string{"a": "o1", "b": "o2", "x": "ox"}
	emb2 := map[string]string{"a": "o2", "b": "o1", "x": "ox"}
	if canonicalKey(p, emb1) == canonicalKey(p, emb2) {
		t.Fatalf("distinct constraint signatures must break symmetry")
	}
}

func TestAutomorphisms_IdentityAlwaysPresent(t *testing.T) {
	p := diamondPattern()
	names := orderedNames(p)
	perms := automorphisms(p, names)
	if len(perms) < 1 {
		t.Fatalf("at least the identity permutation must be present")
	}
	foundIdentity := false
	for _, perm := range perms {
		identical := true
		for i := range perm {
			if perm[i] != names[i] {
				identical = false
				break
			}
		}
		if identical {
			foundIdentity = true
		}
	}
	if !foundIdentity {
		t.Fatalf("identity automorphism missing, got %v", perms)
	}
}
