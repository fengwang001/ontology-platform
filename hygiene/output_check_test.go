package hygiene

import (
	"fmt"
	"strconv"
	"strings"
)

type outTerm struct {
	atom  bool
	text  string
	items []*outTerm
}

func checkOutput(text string) error {
	root, err := parseOutput(text)
	if err != nil {
		return err
	}
	seen := map[int]bool{}
	return checkNode(root, map[string]int{}, seen, map[int]bool{})
}

func parseOutput(text string) (*outTerm, error) {
	p := &outParser{tokens: tokenizeOutput(text)}
	root, err := p.term()
	if err != nil {
		return nil, err
	}
	if p.pos != len(p.tokens) {
		return nil, fmt.Errorf("trailing")
	}
	return root, nil
}

type outParser struct {
	tokens []string
	pos    int
}

func (p *outParser) term() (*outTerm, error) {
	if p.pos >= len(p.tokens) {
		return nil, fmt.Errorf("missing term")
	}
	token := p.tokens[p.pos]
	p.pos++
	if token != "(" {
		return &outTerm{atom: true, text: token}, nil
	}
	list := &outTerm{}
	if p.pos < len(p.tokens) && p.tokens[p.pos] == ")" {
		p.pos++
		return list, nil
	}
	for p.pos >= len(p.tokens) || p.tokens[p.pos] != ")" {
		child, err := p.term()
		if err != nil {
			return nil, err
		}
		list.items = append(list.items, child)
	}
	p.pos++
	return list, nil
}

func tokenizeOutput(text string) []string {
	text = strings.ReplaceAll(text, "(", " ( ")
	text = strings.ReplaceAll(text, ")", " ) ")
	return strings.Fields(text)
}

func checkNode(n *outTerm, scope map[string]int, declared map[int]bool, bound map[int]bool) error {
	if n.atom {
		if base, id, ok := renamed(n.text); ok {
			got, ok := scope[base]
			if !ok || got != id {
				return fmt.Errorf("symbol %s has no matching binder in scope", n.text)
			}
			bound[id] = true
		}
		return nil
	}
	if len(n.items) > 0 && n.items[0].atom && n.items[0].text == "quote" {
		return nil
	}
	if len(n.items) > 0 && n.items[0].atom && n.items[0].text == "lam" {
		if len(n.items) != 3 || len(n.items[1].items) == 0 {
			return fmt.Errorf("bad output lam")
		}
		inner := copyScope(scope)
		var ids []int
		for _, param := range n.items[1].items {
			if !param.atom {
				return fmt.Errorf("non-symbol binder")
			}
			base, id, ok := renamed(param.text)
			if !ok || declared[id] {
				return fmt.Errorf("invalid or duplicate binder %s", param.text)
			}
			declared[id] = true
			inner[base] = id
			ids = append(ids, id)
		}
		return checkNode(n.items[2], inner, declared, bound)
	}
	for _, child := range n.items {
		if err := checkNode(child, scope, declared, bound); err != nil {
			return err
		}
	}
	return nil
}

func renamed(text string) (string, int, bool) {
	index := strings.LastIndex(text, "#")
	if index <= 0 || index == len(text)-1 {
		return "", 0, false
	}
	id, err := strconv.Atoi(text[index+1:])
	if err != nil {
		return "", 0, false
	}
	return text[:index], id, true
}

func copyScope(scope map[string]int) map[string]int {
	copyScope := make(map[string]int, len(scope))
	for key, value := range scope {
		copyScope[key] = value
	}
	return copyScope
}
