package ontology

import "fmt"

// state 是重放日志得到的完整内存图状态。
type state struct {
	objectTypes map[ObjectTypeID]*ObjectType
	linkTypes   map[LinkTypeID]*LinkType
	objects     map[ObjectID]*object
	links       map[linkKey]*link
	permissions map[Permission]bool
}

type object struct {
	id       ObjectID
	typ      ObjectTypeID
	active   bool
	outgoing map[linkKey]*link
	incoming map[linkKey]*link
}

type linkKey struct {
	typ  LinkTypeID
	from ObjectID
	to   ObjectID
}

type link struct {
	key  linkKey
	cost int64
}

func newState() *state {
	return &state{
		objectTypes: make(map[ObjectTypeID]*ObjectType),
		linkTypes:   make(map[LinkTypeID]*LinkType),
		objects:     make(map[ObjectID]*object),
		links:       make(map[linkKey]*link),
		permissions: make(map[Permission]bool),
	}
}

// apply 把一条已被接受的操作应用到状态上（重放与在线路径共用）。
// 前置条件：操作已通过 validate；apply 不做任何拒绝判定。
func (s *state) apply(op Operation) {
	switch op.Kind {
	case OpDeclareObjectType:
		s.objectTypes[op.ObjectType] = &ObjectType{ID: op.ObjectType}
	case OpDeclareLinkType:
		spec := *op.LinkSpec
		s.linkTypes[spec.ID] = &spec
	case OpCreateObject:
		s.objects[op.Object] = &object{
			id:       op.Object,
			typ:      op.ObjectType,
			active:   true,
			outgoing: make(map[linkKey]*link),
			incoming: make(map[linkKey]*link),
		}
	case OpInvalidateObject:
		if o, ok := s.objects[op.Object]; ok {
			o.active = false
			// 失效对象的所有关联链接一并移除。
			for _, l := range o.outgoing {
				s.removeLink(l)
			}
			for _, l := range o.incoming {
				s.removeLink(l)
			}
		}
	case OpCreateLink:
		lt := s.linkTypes[op.LinkType]
		key := linkKey{typ: op.LinkType, from: op.From, to: op.To}
		l := &link{key: key, cost: lt.Cost}
		s.links[key] = l
		s.objects[op.From].outgoing[key] = l
		s.objects[op.To].incoming[key] = l
		if !lt.Directed {
			// 无向链接在两个方向上都可遍历。
			s.objects[op.To].outgoing[key] = l
			s.objects[op.From].incoming[key] = l
		}
	case OpRemoveLink:
		if l, ok := s.links[linkKey{typ: op.LinkType, from: op.From, to: op.To}]; ok {
			s.removeLink(l)
		}
	case OpGrantPermission:
		s.permissions[*op.Permission] = true
	case OpRevokePermission:
		delete(s.permissions, *op.Permission)
	}
}

func (s *state) removeLink(l *link) {
	delete(s.links, l.key)
	if from, ok := s.objects[l.key.from]; ok {
		delete(from.outgoing, l.key)
		delete(from.incoming, l.key)
	}
	if to, ok := s.objects[l.key.to]; ok {
		delete(to.outgoing, l.key)
		delete(to.incoming, l.key)
	}
}

// validate 按四级拒绝次序校验操作：参数非法 > 权限不足 > 基数上限。
// 返回 nil 表示通过全部校验，可以执行并记入日志。
func (s *state) validate(op Operation, caller UserID) *RejectError {
	if r := s.validateParams(op); r != nil {
		return r
	}
	if r := s.validatePermission(op, caller); r != nil {
		return r
	}
	if r := s.validateCardinality(op); r != nil {
		return r
	}
	return nil
}

func (s *state) validateParams(op Operation) *RejectError {
	bad := func(format string, args ...any) *RejectError {
		return &RejectError{Kind: RejectInvalidParams, Detail: fmt.Sprintf(format, args...)}
	}
	switch op.Kind {
	case OpDeclareObjectType:
		if op.ObjectType == "" {
			return bad("object type id required")
		}
		if _, ok := s.objectTypes[op.ObjectType]; ok {
			return bad("object type %q already declared", op.ObjectType)
		}
	case OpDeclareLinkType:
		spec := op.LinkSpec
		if spec == nil {
			return bad("link spec required")
		}
		if spec.ID == "" {
			return bad("link type id required")
		}
		if _, ok := s.linkTypes[spec.ID]; ok {
			return bad("link type %q already declared", spec.ID)
		}
		if spec.Cost <= 0 {
			return bad("link cost must be > 0")
		}
		if spec.MaxOutgoing < 0 || spec.MaxIncoming < 0 {
			return bad("cardinality limits must be >= 0")
		}
		if _, ok := s.objectTypes[spec.FromType]; !ok {
			return bad("unknown from object type %q", spec.FromType)
		}
		if _, ok := s.objectTypes[spec.ToType]; !ok {
			return bad("unknown to object type %q", spec.ToType)
		}
	case OpCreateObject:
		if op.Object == "" {
			return bad("object id required")
		}
		if _, ok := s.objectTypes[op.ObjectType]; !ok {
			return bad("unknown object type %q", op.ObjectType)
		}
		if _, ok := s.objects[op.Object]; ok {
			return bad("object %q already exists", op.Object)
		}
	case OpInvalidateObject:
		o, ok := s.objects[op.Object]
		if !ok {
			return bad("object %q not found", op.Object)
		}
		if !o.active {
			return bad("object %q already invalidated", op.Object)
		}
	case OpCreateLink:
		lt, ok := s.linkTypes[op.LinkType]
		if !ok {
			return bad("unknown link type %q", op.LinkType)
		}
		from, ok := s.objects[op.From]
		if !ok || !from.active {
			return bad("from object %q not found or inactive", op.From)
		}
		to, ok := s.objects[op.To]
		if !ok || !to.active {
			return bad("to object %q not found or inactive", op.To)
		}
		if from.typ != lt.FromType || to.typ != lt.ToType {
			return bad("endpoint types %q->%q do not match link type %q (%q->%q)",
				from.typ, to.typ, lt.ID, lt.FromType, lt.ToType)
		}
		if _, dup := s.links[linkKey{typ: op.LinkType, from: op.From, to: op.To}]; dup {
			return bad("link already exists")
		}
	case OpRemoveLink:
		if _, ok := s.linkTypes[op.LinkType]; !ok {
			return bad("unknown link type %q", op.LinkType)
		}
		if _, ok := s.links[linkKey{typ: op.LinkType, from: op.From, to: op.To}]; !ok {
			return bad("link not found")
		}
	case OpGrantPermission, OpRevokePermission:
		p := op.Permission
		if p == nil {
			return bad("permission required")
		}
		if p.User == "" {
			return bad("permission user required")
		}
		if p.Action != ActionRead && p.Action != ActionWrite {
			return bad("unknown action %q", p.Action)
		}
		switch p.Resource.Kind {
		case ResourceObjectType:
			if _, ok := s.objectTypes[ObjectTypeID(p.Resource.ID)]; !ok {
				return bad("unknown object type %q", p.Resource.ID)
			}
		case ResourceLinkType:
			if _, ok := s.linkTypes[LinkTypeID(p.Resource.ID)]; !ok {
				return bad("unknown link type %q", p.Resource.ID)
			}
		default:
			return bad("unknown resource kind %q", p.Resource.Kind)
		}
		if op.Kind == OpGrantPermission && s.permissions[*p] {
			return bad("permission already granted")
		}
		if op.Kind == OpRevokePermission && !s.permissions[*p] {
			return bad("permission not granted")
		}
	default:
		return bad("unknown op kind %q", op.Kind)
	}
	return nil
}

func (s *state) validatePermission(op Operation, caller UserID) *RejectError {
	deny := func(format string, args ...any) *RejectError {
		return &RejectError{Kind: RejectPermission, Detail: fmt.Sprintf(format, args...)}
	}
	need := func(res Resource) *RejectError {
		if !s.canWrite(caller, res) {
			return deny("caller %q lacks write on %s:%s", caller, res.Kind, res.ID)
		}
		return nil
	}
	switch op.Kind {
	case OpDeclareObjectType, OpDeclareLinkType, OpGrantPermission, OpRevokePermission:
		// 模式变更与授权管理仅管理员可用。
		if caller != AdminUser {
			return deny("caller %q is not admin", caller)
		}
	case OpCreateObject:
		if r := need(Resource{Kind: ResourceObjectType, ID: string(op.ObjectType)}); r != nil {
			return r
		}
	case OpInvalidateObject:
		o := s.objects[op.Object]
		if r := need(Resource{Kind: ResourceObjectType, ID: string(o.typ)}); r != nil {
			return r
		}
	case OpCreateLink, OpRemoveLink:
		if r := need(Resource{Kind: ResourceLinkType, ID: string(op.LinkType)}); r != nil {
			return r
		}
	}
	return nil
}

func (s *state) validateCardinality(op Operation) *RejectError {
	if op.Kind != OpCreateLink {
		return nil
	}
	lt := s.linkTypes[op.LinkType]
	exceed := func(format string, args ...any) *RejectError {
		return &RejectError{Kind: RejectCardinality, Detail: fmt.Sprintf(format, args...)}
	}
	countOut := func(id ObjectID) int {
		n := 0
		for key := range s.objects[id].outgoing {
			if key.typ == lt.ID {
				n++
			}
		}
		return n
	}
	countIn := func(id ObjectID) int {
		n := 0
		for key := range s.objects[id].incoming {
			if key.typ == lt.ID {
				n++
			}
		}
		return n
	}
	if lt.MaxOutgoing > 0 && countOut(op.From) >= lt.MaxOutgoing {
		return exceed("link type %q max outgoing %d exceeded for %q", lt.ID, lt.MaxOutgoing, op.From)
	}
	if lt.MaxIncoming > 0 && countIn(op.To) >= lt.MaxIncoming {
		return exceed("link type %q max incoming %d exceeded for %q", lt.ID, lt.MaxIncoming, op.To)
	}
	return nil
}

// canRead 判断调用者是否对资源有读权限。
func (s *state) canRead(user UserID, res Resource) bool {
	if user == AdminUser {
		return true
	}
	return s.permissions[Permission{User: user, Action: ActionRead, Resource: res}]
}

// canWrite 判断调用者是否对资源有写权限。
func (s *state) canWrite(user UserID, res Resource) bool {
	if user == AdminUser {
		return true
	}
	return s.permissions[Permission{User: user, Action: ActionWrite, Resource: res}]
}
