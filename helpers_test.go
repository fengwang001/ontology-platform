package ontology

import "testing"

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func key(typ, id string) ObjectKey { return ObjectKey{Type: typ, ID: id} }

// newStore 注册测试用 ObjectType：User / Team / Doc / Node。
func newStore(t *testing.T) *Store {
	t.Helper()
	s := NewStore()
	for _, typ := range []string{"User", "Team", "Doc", "Node"} {
		must(t, s.RegisterObjectType(typ))
	}
	return s
}

func addObjects(t *testing.T, s *Store, typ string, ids ...string) {
	t.Helper()
	for _, id := range ids {
		must(t, s.AddObject(typ, id))
	}
}

func declare(t *testing.T, s *Store, lt LinkType) {
	t.Helper()
	must(t, s.DeclareLinkType(lt))
}

func link(t *testing.T, s *Store, lt string, src, tgt ObjectKey) {
	t.Helper()
	must(t, s.Link(lt, src, tgt))
}

func requireKeys(t *testing.T, got []ObjectKey, want ...ObjectKey) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func requireInvariantOK(t *testing.T, s *Store) {
	t.Helper()
	if vs := s.CheckInvariant(); len(vs) > 0 {
		t.Fatalf("invariant violated: %v", vs)
	}
}

// 常用 LinkType 声明。
func ltOwns() LinkType {
	return LinkType{Name: "owns", SourceType: "User", TargetType: "Doc",
		Cardinality: ManyToMany, Cascade: CascadeDelete}
}

func ltMember() LinkType {
	return LinkType{Name: "member", SourceType: "Team", TargetType: "User",
		Cardinality: OneToMany, Cascade: CascadeSetNull}
}

func ltSpouse() LinkType {
	return LinkType{Name: "spouse", SourceType: "User", TargetType: "User",
		Cardinality: OneToOne, Cascade: CascadeSetNull}
}

func ltDepends() LinkType {
	return LinkType{Name: "depends", SourceType: "Node", TargetType: "Node",
		Cardinality: ManyToMany, Cascade: CascadeDelete}
}

func ltGuards() LinkType {
	return LinkType{Name: "guards", SourceType: "Team", TargetType: "Doc",
		Cardinality: ManyToMany, Cascade: CascadeRestrict}
}
