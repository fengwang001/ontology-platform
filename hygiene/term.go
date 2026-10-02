package hygiene

import "regexp"

const (
	reservedLam      = "lam"
	reservedQuote    = "quote"
	maxMacroCount    = 100
	maxMacroArgs     = 8
	maxTemplateNodes = 200
	maxListItems     = 8
	maxSymbolSize    = 32
)

var symbolPattern = regexp.MustCompile(`^[a-z0-9+\-*/<>=!?]{1,32}$`)

type labeledTerm struct {
	isList bool
	name   *labeledSymbol
	items  []*labeledTerm
}

type labelKind int

const (
	labelUser labelKind = iota
	labelGlobal
	labelBinding
	labelRaw
)

type labeledSymbol struct {
	name string
	kind labelKind
	id   int
}

func validSymbolName(name string) bool {
	return len(name) <= maxSymbolSize && symbolPattern.MatchString(name)
}

func reservedName(name string) bool {
	return name == reservedLam || name == reservedQuote
}

func validateTerm(t *Term) bool {
	if t == nil {
		return false
	}
	if t.Kind == Atom {
		return t.Items == nil && validSymbolName(t.Value)
	}
	if t.Kind != List || len(t.Items) > maxListItems {
		return false
	}
	for _, child := range t.Items {
		if !validateTerm(child) {
			return false
		}
	}
	return true
}

func countTermNodes(t *Term) int {
	if t == nil {
		return 0
	}
	count := 1
	for _, child := range t.Items {
		count += countTermNodes(child)
	}
	return count
}

func termToUser(t *Term) *labeledTerm {
	if t.Kind == Atom {
		return &labeledTerm{name: &labeledSymbol{name: t.Value, kind: labelUser}}
	}
	result := &labeledTerm{isList: true}
	for _, child := range t.Items {
		result.items = append(result.items, termToUser(child))
	}
	return result
}

func termToRaw(t *Term) *labeledTerm {
	if t.Kind == Atom {
		return &labeledTerm{name: &labeledSymbol{name: t.Value, kind: labelRaw}}
	}
	result := &labeledTerm{isList: true}
	for _, child := range t.Items {
		result.items = append(result.items, termToRaw(child))
	}
	return result
}

func labeledToTerm(t *labeledTerm) *Term {
	if !t.isList {
		return &Term{Kind: Atom, Value: t.name.name}
	}
	result := &Term{Kind: List}
	for _, child := range t.items {
		result.Items = append(result.Items, labeledToTerm(child))
	}
	return result
}
