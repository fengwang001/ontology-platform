package hygiene

import "strconv"

const (
	maxExpansionDepth = 20
	maxOutputNodes    = 2000
)

type scopeEntry struct {
	name       string
	binding    *binding
	introduced bool
}

type expander struct {
	session    *Session
	scope      []scopeEntry
	nextN      int
	count      int
	expansions int
}

func (s *Session) expandRoot(root *node) (*node, int, int, error) {
	engine := &expander{session: s, nextN: s.globalN + 1}
	result, err := engine.process(root)
	if err != nil {
		return nil, 0, 0, err
	}
	return result, engine.nextN - 1, engine.expansions, nil
}

func (e *expander) process(n *node) (*node, error) {
	if n.atom {
		if err := e.addOutputNode(); err != nil {
			return nil, err
		}
		e.resolveAtom(n)
		return n, nil
	}
	if len(n.items) == 0 {
		return nil, ErrInvalidForm
	}

	head := n.items[0]
	if head.atom && head.text == "quote" {
		if len(n.items) != 2 {
			return nil, ErrInvalidForm
		}
		if err := e.addOutputNode(); err != nil {
			return nil, err
		}
		if err := e.countSubtree(head); err != nil {
			return nil, err
		}
		if err := e.countSubtree(n.items[1]); err != nil {
			return nil, err
		}
		return n, nil
	}
	if head.atom && head.text == "lam" {
		return e.processLam(n)
	}

	if macroDef, args, ok := e.macroApplication(n); ok {
		if len(args) != len(macroDef.params) {
			return nil, ErrArgumentCount
		}
		if n.depth+1 > maxExpansionDepth {
			return nil, ErrDepthLimit
		}
		instantiated := instantiateTemplate(macroDef, args, n.depth+1)
		e.expansions++
		return e.process(instantiated)
	}

	if err := e.addOutputNode(); err != nil {
		return nil, err
	}
	for i, child := range n.items {
		processedChild, err := e.process(child)
		if err != nil {
			return nil, err
		}
		n.items[i] = processedChild
	}
	return n, nil
}

func (e *expander) processLam(n *node) (*node, error) {
	if len(n.items) != 3 || !isList(n.items[1]) || len(n.items[1].items) == 0 {
		return nil, ErrInvalidForm
	}
	params := n.items[1].items
	seen := make(map[string]bool, len(params))
	entries := make([]scopeEntry, 0, len(params))
	for _, param := range params {
		if !param.atom || reservedWords[param.text] || seen[param.text] {
			return nil, ErrInvalidForm
		}
		seen[param.text] = true
		introduced := param.source != sourceUser
		bind := param.binding
		if !introduced || bind == nil {
			bind = &binding{introduced: introduced}
			param.binding = bind
		}
		param.isBinder = true
		entries = append(entries, scopeEntry{
			name:       param.text,
			binding:    bind,
			introduced: introduced,
		})
	}

	if err := e.addOutputNode(); err != nil {
		return nil, err
	}
	if err := e.countSubtree(n.items[0]); err != nil {
		return nil, err
	}
	if err := e.addOutputNode(); err != nil {
		return nil, err
	}
	for _, param := range params {
		if err := e.countSubtree(param); err != nil {
			return nil, err
		}
	}

	previousScope := e.scope
	e.scope = append(append([]scopeEntry(nil), e.scope...), entries...)
	for _, param := range params {
		param.binding.final = e.nextN
		e.nextN++
	}
	processedBody, err := e.process(n.items[2])
	e.scope = previousScope
	if err != nil {
		return nil, err
	}
	n.items[2] = processedBody
	for _, param := range params {
		param.text = baseName(param.text) + "#" + strconv.Itoa(param.binding.final)
	}
	return n, nil
}

func (e *expander) macroApplication(n *node) (macro, []*node, bool) {
	head := n.items[0]
	if !head.atom || head.text == "lam" || head.text == "quote" {
		return macro{}, nil, false
	}
	if head.source == sourceGlobal {
		macroDef, ok := e.session.macros[head.text]
		return macroDef, n.items[1:], ok
	}
	if head.source != sourceUser {
		return macro{}, nil, false
	}
	if e.lookupUser(head.text) != nil {
		return macro{}, nil, false
	}
	macroDef, ok := e.session.macros[head.text]
	return macroDef, n.items[1:], ok
}

func (e *expander) lookupUser(name string) *scopeEntry {
	for i := len(e.scope) - 1; i >= 0; i-- {
		entry := &e.scope[i]
		if entry.name == name && !entry.introduced {
			return entry
		}
	}
	return nil
}

func (e *expander) resolveAtom(n *node) {
	if n.source == sourceUser {
		if entry := e.lookupUser(n.text); entry != nil {
			n.binding = entry.binding
		}
	}
	if n.source != sourceLiteral && n.binding != nil && n.binding.final != 0 {
		n.text = baseName(n.text) + "#" + strconv.Itoa(n.binding.final)
	}
}

func (e *expander) addOutputNode() error {
	e.count++
	if e.count > maxOutputNodes {
		return ErrSizeLimit
	}
	return nil
}

func (e *expander) countSubtree(n *node) error {
	if err := e.addOutputNode(); err != nil {
		return err
	}
	if !n.atom {
		for _, child := range n.items {
			if err := e.countSubtree(child); err != nil {
				return err
			}
		}
	}
	return nil
}

func isList(n *node) bool {
	return n != nil && !n.atom
}

func baseName(text string) string {
	for i := len(text) - 1; i >= 0; i-- {
		if text[i] == '#' {
			return text[:i]
		}
	}
	return text
}
