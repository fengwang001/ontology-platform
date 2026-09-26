// Package reast defines the regular-expression AST and parses patterns
// by the fixed grammar: postfix * + ? > concatenation > alternation.
package reast

import "errors"

// Four pairwise distinct, decidable sentinel errors.
var (
	ErrEmpty       = errors.New("reast: empty pattern")
	ErrIllegalChar = errors.New("reast: illegal character")
	ErrParens      = errors.New("reast: unbalanced parentheses")
	ErrPostfix     = errors.New("reast: postfix operator without operand")
)

type Kind int

const (
	KChar   Kind = iota // literal letter (field Char)
	KAlt                // Subs: alternatives
	KConcat             // Subs: sequence
	KStar               // Subs[0]
	KPlus               // Subs[0], meaning Subs[0]·Subs[0]*
	KQuest              // Subs[0], meaning Subs[0]|epsilon
)

// Node is one AST node.
type Node struct {
	Kind Kind
	Char byte
	Subs []*Node
}

// RE is a parsed regular expression.
type RE struct{ Root *Node }

// Parse is pure: on failure it returns nil, error and touches no state.
func Parse(pattern string) (*RE, error) {
	if pattern == "" {
		return nil, ErrEmpty
	}
	p := &parser{s: pattern}
	root, err := p.alt()
	if err != nil {
		return nil, err
	}
	if p.p < len(p.s) { // only an unmatched ')' can remain
		return nil, ErrParens
	}
	return &RE{Root: root}, nil
}

type parser struct {
	s      string
	p      int
	parens int // count of unmatched '(' currently open
}

func (p *parser) peek() byte {
	if p.p >= len(p.s) {
		return 0
	}
	return p.s[p.p]
}

// alt := seq ('|' seq)*
func (p *parser) alt() (*Node, error) {
	first, err := p.seq()
	if err != nil {
		return nil, err
	}
	subs := []*Node{first}
	for p.peek() == '|' {
		p.p++
		branch, err := p.seq() // an empty branch has no operand
		if err != nil {
			return nil, err
		}
		subs = append(subs, branch)
	}
	if len(subs) == 1 {
		return subs[0], nil
	}
	return &Node{Kind: KAlt, Subs: subs}, nil
}

// seq := atom+
func (p *parser) seq() (*Node, error) {
	var subs []*Node
	for c := p.peek(); c != '|' && c != ')' && c != 0; c = p.peek() {
		n, err := p.atom()
		if err != nil {
			return nil, err
		}
		subs = append(subs, n)
	}
	if len(subs) == 0 {
		if p.peek() == ')' && p.parens == 0 {
			return nil, ErrParens // ')' with no matching '('
		}
		if p.peek() == 0 && p.parens > 0 {
			return nil, ErrParens // input ends with an unclosed '('
		}
		return nil, ErrPostfix // an operand was expected
	}
	if len(subs) == 1 {
		return subs[0], nil
	}
	return &Node{Kind: KConcat, Subs: subs}, nil
}

// atom := (literal | '(' alt ')') ('*' | '+' | '?')*
func (p *parser) atom() (*Node, error) {
	c := p.peek()
	var n *Node
	switch {
	case c >= 'a' && c <= 'z':
		n, p.p = &Node{Kind: KChar, Char: c}, p.p+1
	case c == '(':
		p.p, p.parens = p.p+1, p.parens+1
		inner, err := p.alt()
		if err != nil {
			return nil, err
		}
		if p.peek() != ')' {
			return nil, ErrParens
		}
		p.p, p.parens = p.p+1, p.parens-1
		n = inner
	case c == '*' || c == '+' || c == '?':
		return nil, ErrPostfix
	default:
		return nil, ErrIllegalChar
	}
	for c := p.peek(); c == '*' || c == '+' || c == '?'; c = p.peek() {
		k := map[byte]Kind{'*': KStar, '+': KPlus, '?': KQuest}[c]
		p.p++
		n = &Node{Kind: k, Subs: []*Node{n}}
	}
	return n, nil
}
