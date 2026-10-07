package ontology

// 测试共享辅助。

func baseStore() *Store {
	s := NewStore()
	s.AddObjectType(ObjectType{ID: "A", Properties: []string{"p"}})
	s.AddObjectType(ObjectType{ID: "B", Properties: []string{"q"}})
	s.AddLinkType(LinkType{ID: "ab", From: "A", To: "B"})
	s.AddLinkType(LinkType{ID: "aa", From: "A", To: "A"})
	s.AddLinkType(LinkType{ID: "bb", From: "B", To: "B"})
	return s
}

func putA(s *Store, id InstanceID) {
	s.PutInstance(&Instance{ID: id, Type: "A", Props: map[string]string{"p": "v"}, Version: 1})
}

func putB(s *Store, id InstanceID) {
	s.PutInstance(&Instance{ID: id, Type: "B", Props: map[string]string{"q": "v"}, Version: 1})
}

func allowAll() Authorizer {
	return AuthorizerFunc{
		VisibleFn:   func(SubjectID, InstanceID) bool { return true },
		AuthorizeFn: func(SubjectID, InstanceID, int) bool { return true },
	}
}

func modifyDecl(depth int, mode InvisibleMode, merge MergeMode) ActionDecl {
	return ActionDecl{
		ID:         "act",
		AllowedOps: map[ObjectTypeID][]OpKind{"A": {OpModify, OpCreate}},
		Cascade:    CascadeRule{MaxDepth: depth},
		Invisible:  mode,
		Merge:      merge,
	}
}

func modifyOp(target InstanceID) DirectOp {
	return DirectOp{Kind: OpModify, Type: "A", Target: target, Props: map[string]string{"p": "w"}}
}

func invOf(ops ...DirectOp) Invocation { return Invocation{Action: "act", Ops: ops} }

// chainStore 构造 a0 -> b1 -> b2 -> b3 的链。
func chainStore() *Store {
	s := baseStore()
	putA(s, "a0")
	putB(s, "b1")
	putB(s, "b2")
	putB(s, "b3")
	s.AddLink(Link{Type: "ab", From: "a0", To: "b1"})
	s.AddLink(Link{Type: "bb", From: "b1", To: "b2"})
	s.AddLink(Link{Type: "bb", From: "b2", To: "b3"})
	return s
}
