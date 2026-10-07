package dlabel

import "sort"

// naiveModel 是独立维护的朴素参照实现：
// 单一大互斥锁 + 每版本完整复制全部状态。它故意不做任何优化
// （无增量版本链、无闭包裁剪、无记忆化），作为裁决语义的独立参照，
// 与生产实现在随机生成的属性取值与操作序列上逐项对照。

type naiveState struct {
	version int64
	attrs   map[string]map[string]map[string]Value // ot -> id -> attr -> value
	kinds   map[string]map[string]ValueKind
	rules   map[string]map[string]Expr
	grants  map[grantKey]Grant
}

type naiveModel struct {
	mu       chan struct{} // 容量 1 的信号量当作大锁
	states   []*naiveState // 下标即版本号，每版本一份完整状态
	retained int64
}

func newNaiveModel(retained int64) *naiveModel {
	m := &naiveModel{retained: retained}
	c := make(chan struct{}, 1)
	c <- struct{}{}
	m.mu = c
	s0 := &naiveState{
		attrs:  map[string]map[string]map[string]Value{},
		kinds:  map[string]map[string]ValueKind{},
		rules:  map[string]map[string]Expr{},
		grants: map[grantKey]Grant{},
	}
	m.states = []*naiveState{s0}
	return m
}

func (m *naiveModel) lock()   { <-m.mu }
func (m *naiveModel) unlock() { m.mu <- struct{}{} }

func (s *naiveState) copyState(v int64) *naiveState {
	n := &naiveState{
		version: v,
		attrs:   map[string]map[string]map[string]Value{},
		kinds:   map[string]map[string]ValueKind{},
		rules:   map[string]map[string]Expr{},
		grants:  map[grantKey]Grant{},
	}
	for ot, ids := range s.attrs {
		n.attrs[ot] = map[string]map[string]Value{}
		for id, am := range ids {
			cp := map[string]Value{}
			for a, val := range am {
				cp[a] = val
			}
			n.attrs[ot][id] = cp
		}
	}
	for ot, km := range s.kinds {
		cp := map[string]ValueKind{}
		for a, k := range km {
			cp[a] = k
		}
		n.kinds[ot] = cp
	}
	for ot, rm := range s.rules {
		cp := map[string]Expr{}
		for tag, body := range rm {
			cp[tag] = body
		}
		n.rules[ot] = cp
	}
	for k, g := range s.grants {
		n.grants[k] = g
	}
	return n
}

func (m *naiveModel) current() *naiveState { return m.states[len(m.states)-1] }

func (m *naiveModel) at(v int64) (*naiveState, ErrorCode) {
	cur := m.current().version
	if v < cur-m.retained || int(v) >= len(m.states) {
		return nil, CodeSnapshotUnavailable
	}
	return m.states[v], CodeOK
}

func (m *naiveModel) registerType(ot string, kinds map[string]ValueKind) {
	m.lock()
	defer m.unlock()
	ns := m.current().copyState(m.current().version + 1)
	km := map[string]ValueKind{}
	for a, k := range kinds {
		km[a] = k
	}
	ns.kinds[ot] = km
	ns.rules[ot] = map[string]Expr{}
	ns.attrs[ot] = map[string]map[string]Value{}
	m.states = append(m.states, ns)
}

func (m *naiveModel) begin() int64 {
	m.lock()
	v := m.current().version
	m.unlock()
	return v
}

func naiveEvalExpr(e Expr, s *naiveState, ot, id string, stack map[string]bool) (bool, ErrorCode) {
	switch e.Op {
	case OpAtom:
		switch e.Atom.Kind {
		case AtomConst:
			return e.Atom.Const, CodeOK
		case AtomAttr:
			if _, ok := s.kinds[ot][e.Atom.Attr]; !ok {
				return false, CodeMissingAttribute
			}
			val := NullValue()
			if am, ok := s.attrs[ot][id]; ok {
				if v, ok := am[e.Atom.Attr]; ok {
					val = v
				}
			}
			return compareValues(val, e.Atom.Value, e.Atom.Op), CodeOK
		case AtomTag:
			if stack[e.Atom.Tag] {
				return false, CodeCyclicRule
			}
			body, ok := s.rules[ot][e.Atom.Tag]
			if !ok {
				return false, CodeUnknownTag
			}
			stack[e.Atom.Tag] = true
			v, c := naiveEvalExpr(body, s, ot, id, stack)
			delete(stack, e.Atom.Tag)
			return v, c
		}
	case OpNot:
		v, c := naiveEvalExpr(e.Terms[0], s, ot, id, stack)
		return !v, c
	case OpAnd:
		for _, t := range e.Terms {
			v, c := naiveEvalExpr(t, s, ot, id, stack)
			if c != CodeOK {
				return false, c
			}
			if !v {
				return false, CodeOK
			}
		}
		return true, CodeOK
	case OpOr:
		for _, t := range e.Terms {
			v, c := naiveEvalExpr(t, s, ot, id, stack)
			if c != CodeOK {
				return false, c
			}
			if v {
				return true, CodeOK
			}
		}
		return false, CodeOK
	}
	return false, CodeInvalidRule
}

func naiveCarried(s *naiveState, ot, id string) (map[string]bool, ErrorCode) {
	out := map[string]bool{}
	tags := make([]string, 0)
	for t := range s.rules[ot] {
		tags = append(tags, t)
	}
	sort.Strings(tags)
	for _, t := range tags {
		v, c := naiveEvalExpr(s.rules[ot][t], s, ot, id, map[string]bool{t: true})
		if c != CodeOK {
			return nil, c
		}
		out[t] = v
	}
	return out, CodeOK
}

// naiveDecide 复刻 deny-overrides 合并与 fail-closed 默认。
func naiveDecide(s *naiveState, subject string, carried map[string]bool, attr string, mode grantMode) bool {
	sawAny, sawDeny, sawAllow := false, false, false
	tags := make([]string, 0)
	for t, c := range carried {
		if c {
			tags = append(tags, t)
		}
	}
	sort.Strings(tags)
	for _, tag := range tags {
		g, ok := s.grants[grantKey{subject: subject, tag: tag}]
		if !ok {
			continue
		}
		matched := mode == grantVisibility
		for _, a := range g.Attrs {
			if a == "*" || a == attr {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		eff := g.Read
		if mode == grantWrite {
			eff = g.Write
		}
		if mode == grantVisibility {
			eff = g.Visibility
		}
		sawAny = true
		if eff == EffectDeny {
			sawDeny = true
		} else {
			sawAllow = true
		}
	}
	return sawAny && !sawDeny && sawAllow
}

type naiveReadOutcome struct {
	code   ErrorCode
	attrs  map[string]int // attr -> 0 denied, 1 allowed
	values map[string]Value
}

func (m *naiveModel) read(subject, ot, id string, want []string, v int64) naiveReadOutcome {
	m.lock()
	defer m.unlock()
	s, code := m.at(v)
	if code != CodeOK {
		return naiveReadOutcome{code: code}
	}
	if _, ok := s.kinds[ot]; !ok {
		return naiveReadOutcome{code: CodeUnknownObjectType}
	}
	am, ok := s.attrs[ot][id]
	if !ok {
		return naiveReadOutcome{code: CodeUnknownInstance}
	}
	carried, c := naiveCarried(s, ot, id)
	if c != CodeOK {
		return naiveReadOutcome{code: c}
	}
	if subject != "root" {
		if !naiveDecide(s, subject, carried, "", grantVisibility) {
			return naiveReadOutcome{code: CodeNotVisible}
		}
	}
	out := naiveReadOutcome{code: CodeOK, attrs: map[string]int{}, values: map[string]Value{}}
	for _, attr := range want {
		if _, ok := s.kinds[ot][attr]; !ok {
			return naiveReadOutcome{code: CodeUnknownAttribute}
		}
		if subject == "root" {
			out.attrs[attr] = 1
			out.values[attr] = am[attr]
			continue
		}
		if !naiveDecide(s, subject, carried, attr, grantRead) {
			out.attrs[attr] = 0
			continue
		}
		out.attrs[attr] = 1
		out.values[attr] = am[attr]
	}
	return out
}

func (m *naiveModel) write(subject, ot, id string, values map[string]Value, create bool) ErrorCode {
	m.lock()
	defer m.unlock()
	s := m.current()
	if _, ok := s.kinds[ot]; !ok {
		return CodeUnknownObjectType
	}
	if create {
		if _, ok := s.attrs[ot][id]; ok {
			return CodeInvalidValue
		}
	} else if _, ok := s.attrs[ot][id]; !ok {
		return CodeUnknownInstance
	}
	for attr, val := range values {
		k, ok := s.kinds[ot][attr]
		if !ok {
			return CodeUnknownAttribute
		}
		if val.Kind() != KindNull && val.Kind() != k {
			return CodeInvalidValue
		}
	}
	ns := s.copyState(s.version + 1)
	am := map[string]Value{}
	if !create {
		for a, val := range ns.attrs[ot][id] {
			am[a] = val
		}
	}
	for a := range ns.kinds[ot] {
		if _, ok := am[a]; !ok {
			am[a] = NullValue()
		}
	}
	for attr, val := range values {
		am[attr] = val
	}
	ns.attrs[ot][id] = am
	if subject != "root" {
		carried, c := naiveCarried(ns, ot, id)
		if c != CodeOK {
			return c
		}
		for attr := range values {
			if !naiveDecide(ns, subject, carried, attr, grantWrite) {
				return CodePermissionDenied
			}
		}
	}
	m.states = append(m.states, ns)
	return CodeOK
}

func (m *naiveModel) setRule(ot string, rules []Rule) ErrorCode {
	m.lock()
	defer m.unlock()
	s := m.current()
	if _, ok := s.kinds[ot]; !ok {
		return CodeUnknownObjectType
	}
	for _, r := range rules {
		if r.Tag == "" {
			return CodeInvalidRule
		}
		if err := validateExpr(r.Body); err != nil {
			return errorCode(err)
		}
		for _, attr := range r.referencedAttrs() {
			if _, ok := s.kinds[ot][attr]; !ok {
				return CodeMissingAttribute
			}
		}
	}
	ns := s.copyState(s.version + 1)
	for _, r := range rules {
		ns.rules[ot][r.Tag] = r.Body
	}
	for _, r := range rules {
		for _, tg := range r.referencedTags() {
			if _, ok := ns.rules[ot][tg]; !ok {
				return CodeUnknownTag
			}
		}
	}
	if err := detectCycle(ns.rules[ot]); err != nil {
		return errorCode(err)
	}
	m.states = append(m.states, ns)
	return CodeOK
}

func (m *naiveModel) setGrant(g Grant) {
	m.lock()
	defer m.unlock()
	s := m.current()
	ns := s.copyState(s.version + 1)
	ns.grants[grantKey{subject: g.Subject, tag: g.Tag}] = g
	m.states = append(m.states, ns)
}

func (m *naiveModel) deleteRule(ot, tag string) ErrorCode {
	m.lock()
	defer m.unlock()
	s := m.current()
	if _, ok := s.kinds[ot]; !ok {
		return CodeUnknownObjectType
	}
	if _, ok := s.rules[ot][tag]; !ok {
		return CodeUnknownTag
	}
	for other, body := range s.rules[ot] {
		if other == tag {
			continue
		}
		for _, dep := range exprTags(body) {
			if dep == tag {
				return CodeInvalidRule
			}
		}
	}
	ns := s.copyState(s.version + 1)
	delete(ns.rules[ot], tag)
	m.states = append(m.states, ns)
	return CodeOK
}

func (m *naiveModel) tagCarried(ot, id, tag string, v int64) (bool, ErrorCode) {
	m.lock()
	defer m.unlock()
	s, code := m.at(v)
	if code != CodeOK {
		return false, code
	}
	if _, ok := s.attrs[ot][id]; !ok {
		return false, CodeUnknownInstance
	}
	body, ok := s.rules[ot][tag]
	if !ok {
		return false, CodeUnknownTag
	}
	val, c := naiveEvalExpr(body, s, ot, id, map[string]bool{tag: true})
	if c != CodeOK {
		return false, c
	}
	return val, CodeOK
}
