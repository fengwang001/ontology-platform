package hygiene_test

import (
	"strconv"
	"strings"

	"ontology/hygiene"
)

type ntKind int

const (
	nUser ntKind = iota
	nGlobal
	nBind
	nRaw
)

type nterm struct {
	atom  bool
	text  string
	kind  ntKind
	id    int
	items []*nterm
}

type nscope map[string]*nterm

type nmacro struct {
	params []string
	body   *hygiene.Term
}

type nsess struct {
	macros map[string]nmacro
	n, x   int
	id     int
	size   int
}

type nerr int

const (
	nOK nerr = iota
	nForm
	nArity
	nDepth
	nSize
)

func newN() *nsess { return &nsess{macros: map[string]nmacro{}} }

func (s *nsess) define(name string, params []string, body *hygiene.Term) {
	s.macros[name] = nmacro{append([]string(nil), params...), body}
}

func (s *nsess) run(t *hygiene.Term) (string, nerr, int, int) {
	out, err := s.eval(fromPub(t), 0, nil)
	if err != 0 {
		return "", err, 0, 0
	}
	return nrender(out), 0, s.n, s.x
}

func (s *nsess) eval(t *nterm, depth int, env []nscope) (*nterm, nerr) {
	if depth > 20 {
		return nil, nDepth
	}
	if t.atom {
		if !s.add(1) {
			return nil, nSize
		}
		return natom(t, env), 0
	}
	if nhead(t, "quote") && len(t.items) == 2 {
		if !s.add(ncount(t)) {
			return nil, nSize
		}
		return t, 0
	}
	if len(t.items) > 0 && nhead(t, "quote") {
		return nil, nForm
	}
	if len(t.items) > 0 && nhead(t, "lam") {
		if len(t.items) != 3 || t.items[1].atom || len(t.items[1].items) == 0 || len(t.items[1].items) > 8 {
			return nil, nForm
		}
		if !s.add(2) {
			return nil, nSize
		}
		sc := nscope{}
		ps := &nterm{}
		seen := map[string]bool{}
		for _, p := range t.items[1].items {
			if !p.atom || p.kind == nRaw || nreserved(p.text) || seen[p.text] {
				return nil, nForm
			}
			seen[p.text] = true
			b := &nterm{atom: true, text: p.text, kind: p.kind, id: p.id}
			if b.kind != nBind {
				b.kind = nUser
			}
			sc[p.text] = b
			ps.items = append(ps.items, b)
			if !s.add(1) {
				return nil, nSize
			}
			s.n++
			b.text = b.text + "#" + strconv.Itoa(s.n)
		}
		body, err := s.eval(t.items[2], depth, append(env, sc))
		if err != 0 {
			return nil, err
		}
		return nlist(natomRaw("lam"), ps, body), 0
	}
	if len(t.items) > 0 && t.items[0].atom {
		h := t.items[0]
		if m, ok := s.macros[h.text]; ok && h.kind != nBind && !nbound(h, env) {
			if len(t.items)-1 != len(m.params) {
				return nil, nArity
			}
			s.x++
			return s.eval(s.subst(m, t.items[1:]), depth+1, env)
		}
	}
	if len(t.items) == 0 || len(t.items) > 8 {
		return nil, nForm
	}
	if !s.add(1) {
		return nil, nSize
	}
	out := &nterm{}
	for _, c := range t.items {
		v, err := s.eval(c, depth, env)
		if err != 0 {
			return nil, err
		}
		out.items = append(out.items, v)
	}
	return out, 0
}

func (s *nsess) subst(m nmacro, args []*nterm) *nterm {
	bind := map[string]*nterm{}
	for i, p := range m.params {
		bind[p] = args[i]
	}
	v := nsub(m.body, bind)
	s.mark(v, nil)
	return v
}

func (s *nsess) mark(t *nterm, st []nframe) {
	if t.atom {
		if t.kind == nGlobal {
			for i := len(st) - 1; i >= 0; i-- {
				if st[i].text == t.text {
					t.kind, t.id = nBind, st[i].id
					return
				}
			}
		}
		return
	}
	if nhead(t, "quote") {
		return
	}
	if nhead(t, "lam") && len(t.items) == 3 && !t.items[1].atom {
		ns := append([]nframe(nil), st...)
		for _, p := range t.items[1].items {
			if p.atom && p.kind == nGlobal && !nreserved(p.text) {
				s.id++
				p.kind, p.id = nBind, s.id
				ns = append(ns, nframe{p.text, s.id})
			}
		}
		for i, c := range t.items {
			cs := st
			if i == 2 {
				cs = ns
			}
			s.mark(c, cs)
		}
		return
	}
	for _, c := range t.items {
		s.mark(c, st)
	}
}

type nframe struct {
	text string
	id   int
}

func nsub(t *hygiene.Term, bind map[string]*nterm) *nterm {
	if t.Kind == hygiene.Atom {
		if v, ok := bind[t.Value]; ok {
			return nclone(v)
		}
		return &nterm{atom: true, text: t.Value, kind: nGlobal}
	}
	out := &nterm{}
	if len(t.Items) == 2 && t.Items[0].Kind == hygiene.Atom && t.Items[0].Value == "quote" {
		out.items = []*nterm{{atom: true, text: "quote", kind: nGlobal}, fromRaw(t.Items[1])}
		return out
	}
	for _, c := range t.Items {
		out.items = append(out.items, nsub(c, bind))
	}
	return out
}

func natom(t *nterm, env []nscope) *nterm {
	if t.kind == nGlobal || t.kind == nRaw {
		return t
	}
	if b := nlookup(t, env); b != nil {
		return b
	}
	return t
}

func nlookup(t *nterm, env []nscope) *nterm {
	for i := len(env) - 1; i >= 0; i-- {
		if t.kind == nBind {
			for _, b := range env[i] {
				if b.kind == nBind && b.id == t.id {
					return b
				}
			}
			continue
		}
		if b, ok := env[i][t.text]; ok {
			if t.kind == nUser && b.kind != nUser {
				continue
			}
			return b
		}
	}
	return nil
}

func nbound(t *nterm, env []nscope) bool {
	return t.kind != nGlobal && nlookup(t, env) != nil
}

func (s *nsess) add(n int) bool {
	s.size += n
	return s.size <= 2000
}

func fromPub(t *hygiene.Term) *nterm {
	if t.Kind == hygiene.Atom {
		return &nterm{atom: true, text: t.Value, kind: nUser}
	}
	return nlist(nil, mapPub(t.Items, fromPub)...)
}

func fromRaw(t *hygiene.Term) *nterm {
	if t.Kind == hygiene.Atom {
		return &nterm{atom: true, text: t.Value, kind: nRaw}
	}
	return nlist(nil, mapPub(t.Items, fromRaw)...)
}

func mapPub(items []*hygiene.Term, f func(*hygiene.Term) *nterm) []*nterm {
	out := make([]*nterm, 0, len(items))
	for _, it := range items {
		out = append(out, f(it))
	}
	return out
}

func nlist(first *nterm, rest ...*nterm) *nterm {
	t := &nterm{}
	if first != nil {
		t.items = append(t.items, first)
	}
	t.items = append(t.items, rest...)
	return t
}

func natomRaw(text string) *nterm { return &nterm{atom: true, text: text, kind: nRaw} }

func nhead(t *nterm, text string) bool {
	return !t.atom && len(t.items) > 0 && t.items[0].atom && t.items[0].text == text
}

func nreserved(text string) bool { return text == "lam" || text == "quote" }

func ncount(t *nterm) int {
	n := 1
	for _, c := range t.items {
		n += ncount(c)
	}
	return n
}

func nclone(t *nterm) *nterm {
	if t.atom {
		return &nterm{atom: true, text: t.text, kind: t.kind, id: t.id}
	}
	out := &nterm{}
	for _, c := range t.items {
		out.items = append(out.items, nclone(c))
	}
	return out
}

func nrender(t *nterm) string {
	if t.atom {
		return t.text
	}
	parts := make([]string, 0, len(t.items))
	for _, c := range t.items {
		parts = append(parts, nrender(c))
	}
	return "(" + strings.Join(parts, " ") + ")"
}
