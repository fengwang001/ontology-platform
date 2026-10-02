package hygiene

import (
	"fmt"
	"strconv"
	"strings"
)

func rParse(input string) (*rt, error) {
	p := &rp{input: input}
	term, err := p.term()
	if err != nil {
		return nil, err
	}
	p.space()
	if p.pos != len(p.input) {
		return nil, fmt.Errorf("trailing")
	}
	return term, nil
}

type rp struct {
	input string
	pos   int
}

func (p *rp) term() (*rt, error) {
	p.space()
	if p.pos >= len(p.input) {
		return nil, fmt.Errorf("missing")
	}
	if p.input[p.pos] == ')' {
		return nil, fmt.Errorf("close")
	}
	if p.input[p.pos] != '(' {
		start := p.pos
		for p.pos < len(p.input) && !strings.ContainsRune("() \t\n\r", rune(p.input[p.pos])) {
			p.pos++
		}
		return &rt{atom: true, text: p.input[start:p.pos], kind: rUser}, nil
	}
	p.pos++
	list := &rt{}
	p.space()
	if p.pos < len(p.input) && p.input[p.pos] == ')' {
		p.pos++
		return list, nil
	}
	for {
		child, err := p.term()
		if err != nil {
			return nil, err
		}
		list.items = append(list.items, child)
		p.space()
		if p.pos >= len(p.input) {
			return nil, fmt.Errorf("missing close")
		}
		if p.input[p.pos] == ')' {
			p.pos++
			return list, nil
		}
	}
}

func (p *rp) space() {
	for p.pos < len(p.input) && strings.ContainsRune(" \t\n\r", rune(p.input[p.pos])) {
		p.pos++
	}
}

func rClone(n *rt) *rt {
	c := *n
	if len(n.items) > 0 {
		c.items = make([]*rt, len(n.items))
		for i, child := range n.items {
			c.items[i] = rClone(child)
		}
	}
	return &c
}

func rPrint(n *rt) string {
	if n.atom {
		return n.text
	}
	if len(n.items) == 0 {
		return "()"
	}
	parts := make([]string, len(n.items))
	for i, child := range n.items {
		parts[i] = rPrint(child)
	}
	return "(" + strings.Join(parts, " ") + ")"
}

func rCount(n *rt) int {
	if n.atom {
		return 1
	}
	total := 1
	for _, child := range n.items {
		total += rCount(child)
	}
	return total
}

func rProcess(macros map[string]rm, n *rt, scope []rs, nextN, x, binders, count, depth int) (*rt, int, int, int, error) {
	if n.atom {
		count++
		if count > 2000 {
			return nil, 0, 0, 0, ErrSizeLimit
		}
		rResolve(n, scope)
		return n, x, binders, count, nil
	}
	if len(n.items) == 0 {
		return nil, 0, 0, 0, ErrInvalidForm
	}
	head := n.items[0]
	if head.atom && head.text == "quote" {
		if len(n.items) != 2 {
			return nil, 0, 0, 0, ErrInvalidForm
		}
		count += rCount(n)
		if count > 2000 {
			return nil, 0, 0, 0, ErrSizeLimit
		}
		return n, x, binders, count, nil
	}
	if head.atom && head.text == "lam" {
		if len(n.items) != 3 || len(n.items[1].items) == 0 {
			return nil, 0, 0, 0, ErrInvalidForm
		}
		params := n.items[1].items
		seen := map[string]bool{}
		entries := []rs{}
		for _, param := range params {
			if !param.atom || param.text == "lam" || param.text == "quote" || seen[param.text] {
				return nil, 0, 0, 0, ErrInvalidForm
			}
			seen[param.text] = true
			bind := param.binder
			if bind == nil {
				bind = &rb{base: param.text}
			}
			bind.base = param.text
			param.kind = rBound
			param.binder = bind
			entries = append(entries, rs{name: param.text, bind: bind})
		}
		count += 2 + len(params)
		if count > 2000 {
			return nil, 0, 0, 0, ErrSizeLimit
		}
		for i := range entries {
			entries[i].bind.id = nextN
			nextN++
		}
		body, nx, nb, nc, err := rProcess(macros, n.items[2], append(append([]rs(nil), scope...), entries...), nextN, x, binders+len(entries), count, depth)
		if err != nil {
			return nil, 0, 0, 0, err
		}
		n.items[2] = body
		for i, param := range params {
			param.text = entries[i].bind.base + "#" + strconv.Itoa(entries[i].bind.id)
		}
		return n, nx, nb, nc, nil
	}
	if def, args, ok := rMacroApp(macros, n, scope); ok {
		if len(args) != len(def.params) {
			return nil, 0, 0, 0, ErrArgumentCount
		}
		if depth+1 > 20 {
			return nil, 0, 0, 0, ErrDepthLimit
		}
		expanded := rInstantiate(def.template, def.params, args)
		return rProcess(macros, expanded, scope, nextN, x+1, binders, count, depth+1)
	}
	count++
	if count > 2000 {
		return nil, 0, 0, 0, ErrSizeLimit
	}
	for i, child := range n.items {
		processed, nx, nb, nc, err := rProcess(macros, child, scope, nextN, x, binders, count, depth)
		if err != nil {
			return nil, 0, 0, 0, err
		}
		n.items[i], x, binders, count = processed, nx, nb, nc
	}
	return n, x, binders, count, nil
}

func rMacroApp(macros map[string]rm, n *rt, scope []rs) (rm, []*rt, bool) {
	head := n.items[0]
	if !head.atom || head.text == "lam" || head.text == "quote" {
		return rm{}, nil, false
	}
	if head.kind == rGlobal {
		def, ok := macros[head.text]
		return def, n.items[1:], ok
	}
	if head.kind != rUser || rLookup(head.text, scope) != nil {
		return rm{}, nil, false
	}
	def, ok := macros[head.text]
	return def, n.items[1:], ok
}

func rLookup(name string, scope []rs) *rb {
	for i := len(scope) - 1; i >= 0; i-- {
		if scope[i].name == name {
			return scope[i].bind
		}
	}
	return nil
}

func rResolve(n *rt, scope []rs) {
	if n.kind == rUser {
		if bind := rLookup(n.text, scope); bind != nil {
			n.kind = rBound
			n.bindID = bind.id
		}
	}
	if n.kind == rBound {
		if n.binder != nil {
			n.bindID = n.binder.id
		}
		n.text = n.text + "#" + strconv.Itoa(n.bindID)
	}
}

func rInstantiate(template *rt, paramNames []string, args []*rt) *rt {
	argMap := map[string]*rt{}
	for i, name := range paramNames {
		argMap[name] = args[i]
	}
	return rInst(template, argMap, nil, false)
}

func rInst(n *rt, args map[string]*rt, binders []rs, quoted bool) *rt {
	if n.atom {
		if arg, ok := args[n.text]; ok {
			c := rClone(arg)
			if quoted {
				markRLiteral(c)
			}
			return c
		}
		c := rClone(n)
		if quoted {
			c.kind = rLiteral
			return c
		}
		for i := len(binders) - 1; i >= 0; i-- {
			if binders[i].name == n.text {
				c.kind = rBound
				c.binder = binders[i].bind
				c.bindID = binders[i].bind.id
				return c
			}
		}
		c.kind = rGlobal
		return c
	}
	c := rClone(n)
	if !quoted && len(c.items) == 3 && c.items[0].atom && c.items[0].text == "lam" && len(c.items[1].items) > 0 {
		nextBinders := append([]rs(nil), binders...)
		for i, param := range n.items[1].items {
			if param.atom {
				if _, isArg := args[param.text]; isArg {
					continue
				}
				bind := &rb{id: -(len(nextBinders) + 1), base: param.text}
				binderCopy := c.items[1].items[i]
				binderCopy.kind = rBound
				binderCopy.bindID = bind.id
				binderCopy.binder = bind
				nextBinders = append(nextBinders, rs{name: param.text, bind: bind})
			}
		}
		for i, param := range n.items[1].items {
			if param.atom {
				if _, ok := args[param.text]; ok {
					c.items[1].items[i] = rInst(param, args, nextBinders, false)
				}
			}
		}
		c.items[2] = rInst(n.items[2], args, nextBinders, false)
		c.items[0] = rInst(n.items[0], args, binders, false)
		return c
	}
	if !quoted && len(c.items) > 0 && c.items[0].atom && c.items[0].text == "quote" {
		for i := range c.items {
			c.items[i] = rInst(n.items[i], args, binders, i > 0)
		}
		return c
	}
	for i := range c.items {
		c.items[i] = rInst(n.items[i], args, binders, quoted)
	}
	return c
}

func markRLiteral(n *rt) {
	if n.atom {
		n.kind = rLiteral
	}
	for _, child := range n.items {
		markRLiteral(child)
	}
}
