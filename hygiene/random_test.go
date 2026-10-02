package hygiene_test

import (
	"math/rand"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"

	"ontology/hygiene"
)

var outputPattern = regexp.MustCompile(`^([a-z0-9+\-*/<>=!?]+)#([1-9][0-9]*)$`)

func TestRandomProgramsAgainstNaiveSimulation(t *testing.T) {
	rng := rand.New(rand.NewSource(1163))
	for caseNumber := 0; caseNumber < 2000; caseNumber++ {
		actual := hygiene.NewSession()
		naive := newN()
		names := registerRandomMacros(t, rng, actual, naive)
		input := randomInput(rng, names, 3)

		inputText := mustRenderPublic(t, input)
		var actualResult *hygiene.Term
		var actualErr error
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("case %d actual panic: %v\ninput=%s", caseNumber, recovered, inputText)
				}
			}()
			actualResult, actualErr = actual.Expand(input)
		}()
		naiveText, naiveKind, naiveBindings, naiveExpansions := func() (text string, kind nerr, n, x int) {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("case %d naive panic: %v\ninput=%s\n%s", caseNumber, recovered, inputText, debug.Stack())
				}
			}()
			return naive.run(input)
		}()

		actualText := ""
		if actualResult != nil {
			actualText = mustRenderPublic(t, actualResult)
			verifyLexicalOutput(t, caseNumber, inputText, actualText)
		}

		if !sameResult(actualErr, naiveKind, actualText, naiveText) {
			t.Fatalf("case %d\ninput: %s\nactual: %q %v\nnaive: %q kind=%d\njudgment: source labels disagree",
				caseNumber, inputText, actualText, actualErr, naiveText, naiveKind)
		}
		if actual.BindingCount() != naiveBindings || actual.ExpansionCount() != naiveExpansions {
			t.Fatalf("case %d state actual N=%d X=%d, naive N=%d X=%d",
				caseNumber, actual.BindingCount(), actual.ExpansionCount(), naiveBindings, naiveExpansions)
		}

		judgment := "accepted; lexical output and user/global/introduced labels match naive model"
		if actualErr != nil {
			judgment = "rejected; first violation class matches naive model and counts stayed unchanged"
		}
		t.Logf("case=%d\ninput=%s\noutput=%s\njudgment=%s", caseNumber, inputText, actualText, judgment)
	}
}

func registerRandomMacros(t *testing.T, rng *rand.Rand, actual *hygiene.Session, naive *nsess) []string {
	t.Helper()
	names := []string{}
	for index := 0; index < 5; index++ {
		name := "m" + strconv.Itoa(index)
		params := []string{}
		for parameter := 0; parameter < rng.Intn(3); parameter++ {
			params = append(params, "p"+strconv.Itoa(parameter))
		}
		body := randomTemplate(rng, name, names, params, 2)
		if err := actual.DefMacro(name, params, body); err != nil {
			t.Fatalf("DefMacro %s: %v", name, err)
		}
		naive.define(name, params, body)
		names = append(names, name)
	}
	return names
}

func randomTemplate(rng *rand.Rand, current string, earlier []string, params []string, depth int) *hygiene.Term {
	if depth == 0 || rng.Intn(3) == 0 {
		return randomTemplateAtom(rng, current, earlier, params)
	}
	switch rng.Intn(8) {
	case 0:
		binder := randomTemplateAtom(rng, current, earlier, params)
		for binder.Kind != hygiene.Atom || binder.Value == "lam" || binder.Value == "quote" {
			binder = atom(randomTemplateName(rng))
		}
		return list(atom("lam"), list(binder), randomTemplate(rng, current, earlier, params, depth-1))
	case 1:
		return list(atom("quote"), randomQuotedDatum(rng, 2))
	case 2:
		return list(atom("lam"), list(atom(randomTemplateName(rng))),
			list(atom("lam"), list(atom(randomTemplateName(rng))), randomTemplate(rng, current, earlier, params, depth-1)))
	default:
		count := 1 + rng.Intn(4)
		items := make([]*hygiene.Term, 0, count)
		items = append(items, randomTemplateAtom(rng, current, earlier, params))
		for range count - 1 {
			items = append(items, randomTemplate(rng, current, earlier, params, depth-1))
		}
		return &hygiene.Term{Kind: hygiene.List, Items: items}
	}
}

func randomTemplateAtom(rng *rand.Rand, current string, earlier []string, params []string) *hygiene.Term {
	choices := []string{"p0", "p1", "p2", "g", "if", "lam", "quote"}
	choices = append(choices, earlier...)
	if rng.Intn(3) == 0 && len(params) > 0 {
		return atom(params[rng.Intn(len(params))])
	}
	return atom(choices[rng.Intn(len(choices))])
}

func randomTemplateName(rng *rand.Rand) string {
	return []string{"t", "u", "v", "z", "f", "g"}[rng.Intn(6)]
}

func randomQuotedDatum(rng *rand.Rand, depth int) *hygiene.Term {
	if depth == 0 || rng.Intn(2) == 0 {
		return atom([]string{"m0", "p0", "lam", "quote", "x"}[rng.Intn(5)])
	}
	items := []*hygiene.Term{}
	for range 1 + rng.Intn(4) {
		items = append(items, randomQuotedDatum(rng, depth-1))
	}
	return &hygiene.Term{Kind: hygiene.List, Items: items}
}

func randomInput(rng *rand.Rand, macros []string, depth int) *hygiene.Term {
	if depth == 0 || rng.Intn(4) == 0 {
		return atom([]string{"x", "t", "f", "g", "m0", "add", "1", "2"}[rng.Intn(8)])
	}
	switch rng.Intn(8) {
	case 0:
		binder := []string{"x", "t", "f", "m0", "m1"}[rng.Intn(5)]
		return list(atom("lam"), list(atom(binder)), randomInput(rng, macros, depth-1))
	case 1:
		return list(atom("quote"), randomQuotedDatum(rng, 2))
	case 2:
		return &hygiene.Term{Kind: hygiene.List}
	default:
		headName := []string{"f", "g", "if", macros[rng.Intn(len(macros))]}[rng.Intn(4)]
		count := rng.Intn(4)
		items := []*hygiene.Term{atom(headName)}
		for range count {
			items = append(items, randomInput(rng, macros, depth-1))
		}
		return &hygiene.Term{Kind: hygiene.List, Items: items}
	}
}

func atom(text string) *hygiene.Term { return &hygiene.Term{Kind: hygiene.Atom, Value: text} }
func list(items ...*hygiene.Term) *hygiene.Term {
	return &hygiene.Term{Kind: hygiene.List, Items: items}
}

func mustRenderPublic(t *testing.T, term *hygiene.Term) string {
	t.Helper()
	text, err := hygiene.Render(term)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

func sameResult(actualErr error, naiveKind nerr, actualText, naiveText string) bool {
	if actualErr == nil {
		return naiveKind == 0 && actualText == naiveText
	}
	return actualErr.(hygiene.ExpandError).Kind == mapNaiveError(naiveKind)
}

func mapNaiveError(kind nerr) hygiene.ExpandErrorKind {
	return []hygiene.ExpandErrorKind{0, hygiene.FormInvalid, hygiene.ArityMismatch, hygiene.DepthExceeded, hygiene.SizeExceeded}[kind]
}

func verifyLexicalOutput(t *testing.T, caseNumber int, input, output string) {
	t.Helper()
	tree, err := parseOutput(output)
	if err != nil {
		t.Fatalf("case %d independent parse failed for %q: %v", caseNumber, output, err)
	}
	if err := checkOutputScopes(tree, nil); err != nil {
		t.Fatalf("case %d independent scope failed for %q: %v", caseNumber, output, err)
	}
	numbers := map[string]bool{}
	if err := checkBinderNumbers(tree, numbers); err != nil {
		t.Fatalf("case %d independent numbering failed for %q: %v", caseNumber, output, err)
	}
	_ = input
}

type outNode struct {
	atom  bool
	text  string
	items []*outNode
}

func parseOutput(text string) (*outNode, error) {
	tokens, err := lexOutput(text)
	if err != nil {
		return nil, err
	}
	position := 0
	node, err := readOutput(tokens, &position)
	if err != nil || position != len(tokens) {
		return nil, strconv.ErrSyntax
	}
	return node, nil
}

func readOutput(tokens []outputToken, position *int) (*outNode, error) {
	if *position >= len(tokens) {
		return nil, strconv.ErrSyntax
	}
	token := tokens[*position]
	*position++
	if !token.open {
		if token.close {
			return nil, strconv.ErrSyntax
		}
		return &outNode{atom: true, text: token.text}, nil
	}
	list := &outNode{}
	for *position < len(tokens) && !tokens[*position].close {
		child, err := readOutput(tokens, position)
		if err != nil {
			return nil, err
		}
		list.items = append(list.items, child)
	}
	if *position >= len(tokens) {
		return nil, strconv.ErrSyntax
	}
	*position++
	return list, nil
}

type outputToken struct {
	text  string
	open  bool
	close bool
}

func lexOutput(text string) ([]outputToken, error) {
	tokens := []outputToken{}
	for i := 0; i < len(text); {
		switch text[i] {
		case '(':
			tokens = append(tokens, outputToken{open: true})
			i++
		case ' ', '\t', '\n', '\r':
			i++
		case ')':
			tokens = append(tokens, outputToken{close: true})
			i++
		default:
			start := i
			for i < len(text) && text[i] != '(' && text[i] != ')' && text[i] != ' ' {
				i++
			}
			word := text[start:i]
			if word == "" {
				return nil, strconv.ErrSyntax
			}
			if !validPublicSymbol(word) && !outputPattern.MatchString(word) {
				return nil, strconv.ErrSyntax
			}
			tokens = append(tokens, outputToken{text: word})
		}
	}
	return tokens, nil
}

func checkOutputScopes(t *outNode, env []map[string]bool) error {
	if t.atom {
		if strings.Contains(t.text, "#") {
			for i := len(env) - 1; i >= 0; i-- {
				if env[i][t.text] {
					return nil
				}
			}
			return strconv.ErrSyntax
		}
		return nil
	}
	if len(t.items) >= 2 && t.items[0].atom && t.items[0].text == "quote" {
		return nil
	}
	if len(t.items) == 3 && t.items[0].atom && t.items[0].text == "lam" {
		params := t.items[1]
		if params.atom || len(params.items) == 0 {
			return strconv.ErrSyntax
		}
		scope := map[string]bool{}
		for _, param := range params.items {
			if !param.atom || !strings.Contains(param.text, "#") || scope[param.text] {
				return strconv.ErrSyntax
			}
			scope[param.text] = true
		}
		return checkOutputScopes(t.items[2], append(env, scope))
	}
	for _, child := range t.items {
		if err := checkOutputScopes(child, env); err != nil {
			return err
		}
	}
	return nil
}

func checkBinderNumbers(t *outNode, seen map[string]bool) error {
	if t.atom {
		return nil
	}
	if len(t.items) == 2 && t.items[0].atom && t.items[0].text == "quote" {
		return nil
	}
	if len(t.items) == 3 && t.items[0].atom && t.items[0].text == "lam" {
		for _, param := range t.items[1].items {
			if seen[param.text] {
				return strconv.ErrSyntax
			}
			seen[param.text] = true
		}
	}
	for _, child := range t.items {
		if err := checkBinderNumbers(child, seen); err != nil {
			return err
		}
	}
	return nil
}

func validPublicSymbol(word string) bool {
	if word == "" || len(word) > 32 {
		return false
	}
	for _, r := range word {
		if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyz0123456789+-*/<>=!?", r) {
			return false
		}
	}
	return true
}
