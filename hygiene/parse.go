package hygiene

import (
	"fmt"
)

const (
	maxSymbolBytes   = 32
	maxListItems     = 8
	maxTemplateNodes = 200
)

var reservedWords = map[string]bool{
	"lam":   true,
	"quote": true,
}

func parseTerm(input string, countLimit int) (*node, int, error) {
	p := parser{input: input, countLimit: countLimit}
	term, err := p.parseNode()
	if err != nil {
		return nil, 0, err
	}
	p.skipSpaces()
	if p.pos != len(p.input) {
		return nil, 0, fmt.Errorf("unexpected trailing input")
	}
	return term, p.count, nil
}

type parser struct {
	input      string
	pos        int
	count      int
	countLimit int
}

func (p *parser) parseNode() (*node, error) {
	p.skipSpaces()
	if p.pos >= len(p.input) {
		return nil, fmt.Errorf("missing term")
	}
	switch p.input[p.pos] {
	case '(':
		return p.parseList()
	case ')':
		return nil, fmt.Errorf("unexpected )")
	default:
		return p.parseSymbol()
	}
}

func (p *parser) parseList() (*node, error) {
	list := &node{}
	if err := p.addNode(list); err != nil {
		return nil, err
	}
	p.pos++
	p.skipSpaces()
	if p.pos < len(p.input) && p.input[p.pos] == ')' {
		p.pos++
		return list, nil
	}
	for {
		child, err := p.parseNode()
		if err != nil {
			return nil, err
		}
		list.items = append(list.items, child)
		if len(list.items) > maxListItems {
			return nil, fmt.Errorf("list has more than %d elements", maxListItems)
		}
		p.skipSpaces()
		if p.pos >= len(p.input) {
			return nil, fmt.Errorf("missing )")
		}
		if p.input[p.pos] == ')' {
			p.pos++
			return list, nil
		}
	}
}

func (p *parser) parseSymbol() (*node, error) {
	start := p.pos
	for p.pos < len(p.input) {
		ch := p.input[p.pos]
		if ch == '(' || ch == ')' || ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
			break
		}
		p.pos++
	}
	text := p.input[start:p.pos]
	if !validSymbol(text) {
		return nil, fmt.Errorf("invalid symbol %q", text)
	}
	sym := &node{atom: true, text: text, source: sourceUser}
	if err := p.addNode(sym); err != nil {
		return nil, err
	}
	return sym, nil
}

func (p *parser) addNode(n *node) error {
	p.count++
	if p.countLimit > 0 && p.count > p.countLimit {
		return fmt.Errorf("term node limit exceeded")
	}
	return nil
}

func (p *parser) skipSpaces() {
	for p.pos < len(p.input) {
		ch := p.input[p.pos]
		if ch != ' ' && ch != '\t' && ch != '\n' && ch != '\r' {
			return
		}
		p.pos++
	}
}

func validSymbol(text string) bool {
	if len(text) < 1 || len(text) > maxSymbolBytes {
		return false
	}
	for i := 0; i < len(text); i++ {
		ch := text[i]
		if ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' {
			continue
		}
		switch ch {
		case '+', '-', '*', '/', '<', '>', '=', '!', '?':
		default:
			return false
		}
	}
	return true
}

func printTerm(n *node) string {
	if n.atom {
		return n.text
	}
	if len(n.items) == 0 {
		return "()"
	}
	out := make([]byte, 0, len(n.items)*4)
	out = append(out, '(')
	for i, child := range n.items {
		if i > 0 {
			out = append(out, ' ')
		}
		out = append(out, printTerm(child)...)
	}
	out = append(out, ')')
	return string(out)
}
