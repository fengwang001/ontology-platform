package hygiene

import "sync"

type TermKind int

const (
	Atom TermKind = iota
	List
)

type Term struct {
	Kind  TermKind
	Value string
	Items []*Term
}

type DefErrorKind int

const (
	DefInvalid DefErrorKind = iota
	DefDuplicate
	DefLimit
)

type ExpandErrorKind int

const (
	FormInvalid ExpandErrorKind = iota
	ArityMismatch
	DepthExceeded
	SizeExceeded
)

type DefError struct{ Kind DefErrorKind }
type ExpandError struct{ Kind ExpandErrorKind }

func (e DefError) Error() string {
	return []string{"invalid macro definition", "duplicate macro name", "macro table limit"}[e.Kind]
}

func (e ExpandError) Error() string {
	return []string{"invalid form", "macro argument count mismatch", "expansion depth exceeded", "output size exceeded"}[e.Kind]
}

type Session struct {
	mu         sync.Mutex
	macros     map[string]macro
	bindings   int
	expansions int
}

type macro struct {
	name   string
	params []string
	body   *Term
}

func NewSession() *Session {
	return &Session{macros: make(map[string]macro)}
}

func (s *Session) DefMacro(name string, params []string, template *Term) error {
	if !validSymbolName(name) || reservedName(name) {
		return DefError{Kind: DefInvalid}
	}
	if len(params) > maxMacroArgs {
		return DefError{Kind: DefInvalid}
	}
	seenParams := make(map[string]struct{}, len(params))
	for _, param := range params {
		if !validSymbolName(param) || reservedName(param) {
			return DefError{Kind: DefInvalid}
		}
		if _, exists := seenParams[param]; exists {
			return DefError{Kind: DefInvalid}
		}
		seenParams[param] = struct{}{}
	}
	if !validateTerm(template) || countTermNodes(template) > maxTemplateNodes {
		return DefError{Kind: DefInvalid}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.macros[name]; exists {
		return DefError{Kind: DefDuplicate}
	}
	if len(s.macros) >= maxMacroCount {
		return DefError{Kind: DefLimit}
	}

	paramsCopy := append([]string(nil), params...)
	s.macros[name] = macro{name: name, params: paramsCopy, body: cloneTerm(template)}
	return nil
}

func (s *Session) BindingCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bindings
}

func (s *Session) ExpansionCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.expansions
}

func cloneTerm(t *Term) *Term {
	if t == nil {
		return nil
	}
	result := &Term{Kind: t.Kind, Value: t.Value}
	for _, child := range t.Items {
		result.Items = append(result.Items, cloneTerm(child))
	}
	return result
}
