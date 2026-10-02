package hygiene

import (
	"errors"
	"sync"
)

var (
	ErrInvalidForm       = errors.New("invalid form")
	ErrArgumentCount     = errors.New("macro argument count mismatch")
	ErrDepthLimit        = errors.New("expansion depth limit exceeded")
	ErrSizeLimit         = errors.New("output size limit exceeded")
	ErrInvalidDefinition = errors.New("invalid macro definition")
	ErrDuplicateMacro    = errors.New("duplicate macro name")
	ErrMacroTableFull    = errors.New("macro table limit exceeded")
)

type Session struct {
	mu       sync.Mutex
	macros   map[string]macro
	order    []string
	globalN  int
	expanded int
}

func NewSession() *Session {
	return &Session{macros: make(map[string]macro)}
}

func (s *Session) DefMacro(name string, params []string, template string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !validSymbol(name) || reservedWords[name] {
		return ErrInvalidDefinition
	}
	if len(params) > maxListItems {
		return ErrInvalidDefinition
	}
	seen := make(map[string]bool, len(params))
	for _, param := range params {
		if !validSymbol(param) || reservedWords[param] || seen[param] {
			return ErrInvalidDefinition
		}
		seen[param] = true
	}
	parsed, _, err := parseTerm(template, maxTemplateNodes)
	if err != nil {
		return ErrInvalidDefinition
	}
	if _, exists := s.macros[name]; exists {
		return ErrDuplicateMacro
	}
	if len(s.macros) >= 100 {
		return ErrMacroTableFull
	}
	paramsCopy := append([]string(nil), params...)
	s.macros[name] = macro{name: name, params: paramsCopy, template: cloneNode(parsed)}
	s.order = append(s.order, name)
	return nil
}

func (s *Session) Expand(input string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	root, _, err := parseTerm(input, 0)
	if err != nil {
		return "", ErrInvalidForm
	}
	result, nextN, expansions, err := s.expandRoot(root)
	if err != nil {
		return "", err
	}
	s.globalN = nextN
	s.expanded += expansions
	return printTerm(result), nil
}

func (s *Session) N() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.globalN
}

func (s *Session) X() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.expanded
}
