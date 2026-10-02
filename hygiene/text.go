package hygiene

import (
	"fmt"
	"strconv"
	"strings"
)

func Parse(text string) (*Term, error) {
	tokens, err := tokenize(text)
	if err != nil {
		return nil, err
	}
	parser := &textParser{tokens: tokens}
	result, err := parser.parseTerm()
	if err != nil {
		return nil, err
	}
	if parser.position != len(parser.tokens) {
		return nil, fmt.Errorf("unexpected trailing input")
	}
	return result, nil
}

func Render(t *Term) (string, error) {
	if t == nil {
		return "", fmt.Errorf("nil term")
	}
	if t.Kind == Atom {
		if t.Items != nil || !validOutputName(t.Value) {
			return "", fmt.Errorf("invalid atom")
		}
		return t.Value, nil
	}
	if t.Kind != List || len(t.Items) > maxListItems {
		return "", fmt.Errorf("invalid list")
	}
	parts := make([]string, 0, len(t.Items))
	for _, child := range t.Items {
		text, err := Render(child)
		if err != nil {
			return "", err
		}
		parts = append(parts, text)
	}
	return "(" + strings.Join(parts, " ") + ")", nil
}

func validOutputName(name string) bool {
	if validSymbolName(name) {
		return true
	}
	index := strings.LastIndex(name, "#")
	if index <= 0 {
		return false
	}
	number, err := strconv.Atoi(name[index+1:])
	return err == nil && number > 0 && validSymbolName(name[:index])
}

type textToken struct {
	text  string
	open  bool
	close bool
}

type textParser struct {
	tokens   []textToken
	position int
}

func tokenize(text string) ([]textToken, error) {
	var tokens []textToken
	for index := 0; index < len(text); {
		switch text[index] {
		case ' ', '\t', '\n', '\r':
			index++
		case '(':
			tokens = append(tokens, textToken{open: true})
			index++
		case ')':
			tokens = append(tokens, textToken{close: true})
			index++
		default:
			start := index
			for index < len(text) {
				char := text[index]
				if char == ' ' || char == '\t' || char == '\n' || char == '\r' || char == '(' || char == ')' {
					break
				}
				index++
			}
			tokens = append(tokens, textToken{text: text[start:index]})
		}
	}
	return tokens, nil
}

func (p *textParser) parseTerm() (*Term, error) {
	if p.position >= len(p.tokens) {
		return nil, fmt.Errorf("unexpected end of input")
	}
	token := p.tokens[p.position]
	p.position++
	if token.close {
		return nil, fmt.Errorf("unexpected )")
	}
	if !token.open {
		if !validSymbolName(token.text) {
			return nil, fmt.Errorf("invalid symbol %q", token.text)
		}
		return &Term{Kind: Atom, Value: token.text}, nil
	}

	list := &Term{Kind: List}
	for p.position >= len(p.tokens) || !p.tokens[p.position].close {
		if p.position >= len(p.tokens) {
			return nil, fmt.Errorf("unterminated list")
		}
		child, err := p.parseTerm()
		if err != nil {
			return nil, err
		}
		list.Items = append(list.Items, child)
		if len(list.Items) > maxListItems {
			return nil, fmt.Errorf("list has too many elements")
		}
	}
	p.position++
	return list, nil
}
