package hygiene

import "fmt"

type scope map[string]*labeledSymbol

type processor struct {
	macros          map[string]macro
	macroCount      int
	nextIntroID     int
	nextOutputIndex int
	nodes           int
}

func (s *Session) Expand(input *Term) (*Term, error) {
	if !validateTerm(input) {
		return nil, ExpandError{Kind: FormInvalid}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	macros := make(map[string]macro, len(s.macros))
	for name, definition := range s.macros {
		macros[name] = definition
	}
	p := &processor{
		macros:          macros,
		nextOutputIndex: s.bindings,
	}

	result, err := p.process(termToUser(cloneTerm(input)), 0, nil, true)
	if err != nil {
		return nil, err
	}

	s.bindings = p.nextOutputIndex
	s.expansions += p.macroCount
	return labeledToTerm(result), nil
}

func (p *processor) process(t *labeledTerm, expansionDepth int, environment []scope, count bool) (*labeledTerm, error) {
	if expansionDepth > 20 {
		return nil, ExpandError{Kind: DepthExceeded}
	}

	if !t.isList {
		if count {
			p.nodes++
			if p.nodes > 2000 {
				return nil, ExpandError{Kind: SizeExceeded}
			}
		}
		return p.processAtom(t, environment), nil
	}

	if isQuote(t) {
		if count {
			p.nodes += countLabeledNodes(t)
			if p.nodes > 2000 {
				return nil, ExpandError{Kind: SizeExceeded}
			}
		}
		return t, nil
	}
	if t.isList && len(t.items) > 0 && !t.items[0].isList && t.items[0].name.name == reservedQuote {
		return nil, ExpandError{Kind: FormInvalid}
	}

	if isLam(t) {
		if len(t.items) != 3 || !t.items[1].isList || len(t.items[1].items) == 0 {
			return nil, ExpandError{Kind: FormInvalid}
		}
		params := t.items[1]
		if !params.isList || len(params.items) > maxListItems {
			return nil, ExpandError{Kind: FormInvalid}
		}

		if count {
			p.nodes += 2
			if p.nodes > 2000 {
				return nil, ExpandError{Kind: SizeExceeded}
			}
		}

		bindings := make(map[string]*labeledSymbol)
		numberedParams := &labeledTerm{isList: true}
		for _, param := range params.items {
			if param.isList || param.name.kind == labelRaw {
				return nil, ExpandError{Kind: FormInvalid}
			}
			if reservedName(param.name.name) {
				return nil, ExpandError{Kind: FormInvalid}
			}
			if _, exists := bindings[param.name.name]; exists {
				return nil, ExpandError{Kind: FormInvalid}
			}

			symbol := param.name
			if symbol.kind == labelBinding {
				symbol = &labeledSymbol{name: symbol.name, kind: labelBinding, id: symbol.id}
			} else {
				symbol = &labeledSymbol{name: symbol.name, kind: labelUser}
			}
			bindings[param.name.name] = symbol
			numberedParams.items = append(numberedParams.items, &labeledTerm{name: symbol})
			if count {
				p.nodes++
				if p.nodes > 2000 {
					return nil, ExpandError{Kind: SizeExceeded}
				}
			}

			p.nextOutputIndex++
			symbol.name = fmt.Sprintf("%s#%d", symbol.name, p.nextOutputIndex)
		}

		body, err := p.process(t.items[2], expansionDepth, append(environment, bindings), count)
		if err != nil {
			return nil, err
		}
		return &labeledTerm{
			isList: true,
			items: []*labeledTerm{
				{name: &labeledSymbol{name: reservedLam, kind: labelRaw}},
				numberedParams,
				body,
			},
		}, nil
	}

	if len(t.items) > 0 && !t.items[0].isList {
		head := t.items[0].name
		if definition, exists := p.macros[head.name]; exists && head.kind != labelBinding && !p.locallyBound(head, environment) {
			if len(t.items)-1 != len(definition.params) {
				return nil, ExpandError{Kind: ArityMismatch}
			}
			p.macroCount++
			instance := p.instantiate(definition, t.items[1:])
			result, err := p.process(instance, expansionDepth+1, environment, false)
			if err != nil {
				return nil, err
			}
			if count {
				p.nodes += countLabeledNodes(result)
				if p.nodes > 2000 {
					return nil, ExpandError{Kind: SizeExceeded}
				}
			}
			return result, nil
		}
	}

	if len(t.items) == 0 || len(t.items) > maxListItems {
		return nil, ExpandError{Kind: FormInvalid}
	}
	if count {
		p.nodes++
		if p.nodes > 2000 {
			return nil, ExpandError{Kind: SizeExceeded}
		}
	}

	items := make([]*labeledTerm, 0, len(t.items))
	for _, child := range t.items {
		processed, err := p.process(child, expansionDepth, environment, count)
		if err != nil {
			return nil, err
		}
		items = append(items, processed)
	}
	return &labeledTerm{isList: true, items: items}, nil
}

func (p *processor) processAtom(t *labeledTerm, environment []scope) *labeledTerm {
	if t.name.kind == labelGlobal || t.name.kind == labelRaw {
		return t
	}
	if resolved := p.resolve(t.name, environment); resolved != nil {
		return &labeledTerm{name: resolved}
	}
	return t
}

func (p *processor) resolve(symbol *labeledSymbol, environment []scope) *labeledSymbol {
	for index := len(environment) - 1; index >= 0; index-- {
		if symbol.kind == labelBinding {
			for _, binding := range environment[index] {
				if binding.kind == labelBinding && binding.id == symbol.id {
					return binding
				}
			}
			continue
		}
		if binding, exists := environment[index][symbol.name]; exists {
			if symbol.kind == labelUser && binding.kind != labelUser {
				continue
			}
			return binding
		}
	}
	return nil
}

func (p *processor) locallyBound(symbol *labeledSymbol, environment []scope) bool {
	if symbol.kind == labelGlobal {
		return false
	}
	return p.resolve(symbol, environment) != nil
}

func (p *processor) instantiate(definition macro, args []*labeledTerm) *labeledTerm {
	paramArgs := make(map[string]*labeledTerm, len(definition.params))
	for index, name := range definition.params {
		paramArgs[name] = args[index]
	}
	instance := substituteTemplate(cloneTerm(definition.body), paramArgs)
	classifyIntroductions(instance, nil, p)
	return instance
}

func substituteTemplate(t *Term, args map[string]*labeledTerm) *labeledTerm {
	if t.Kind == Atom {
		if arg, exists := args[t.Value]; exists {
			return cloneLabeled(arg)
		}
		return &labeledTerm{name: &labeledSymbol{name: t.Value, kind: labelGlobal}}
	}

	result := &labeledTerm{isList: true}
	if len(t.Items) == 2 && t.Items[0].Kind == Atom && t.Items[0].Value == reservedQuote {
		result.items = append(result.items,
			&labeledTerm{name: &labeledSymbol{name: reservedQuote, kind: labelGlobal}},
			termToRaw(t.Items[1]),
		)
		return result
	}

	for _, child := range t.Items {
		result.items = append(result.items, substituteTemplate(child, args))
	}
	return result
}

type introFrame struct {
	name string
	id   int
}

func classifyIntroductions(t *labeledTerm, stack []introFrame, p *processor) error {
	if !t.isList {
		if t.name.kind == labelGlobal {
			for index := len(stack) - 1; index >= 0; index-- {
				if stack[index].name == t.name.name {
					t.name.kind = labelBinding
					t.name.id = stack[index].id
					break
				}
			}
		}
		return nil
	}
	if isQuote(t) {
		return nil
	}
	if isLam(t) && len(t.items) == 3 {
		params := t.items[1]
		if !params.isList {
			return nil
		}
		nextStack := append([]introFrame(nil), stack...)
		for _, param := range params.items {
			if !param.isList && param.name.kind == labelGlobal && !reservedName(param.name.name) {
				p.nextIntroID++
				id := p.nextIntroID
				param.name.kind = labelBinding
				param.name.id = id
				nextStack = append(nextStack, introFrame{name: param.name.name, id: id})
			}
		}
		for index, child := range t.items {
			childStack := nextStack
			if index != 2 {
				childStack = stack
			}
			if err := classifyIntroductions(child, childStack, p); err != nil {
				return err
			}
		}
		return nil
	}
	for _, child := range t.items {
		if err := classifyIntroductions(child, stack, p); err != nil {
			return err
		}
	}
	return nil
}

func isQuote(t *labeledTerm) bool {
	return t.isList && len(t.items) == 2 && !t.items[0].isList && t.items[0].name.name == reservedQuote
}

func isLam(t *labeledTerm) bool {
	return t.isList && len(t.items) >= 1 && !t.items[0].isList && t.items[0].name.name == reservedLam
}

func countLabeledNodes(t *labeledTerm) int {
	count := 1
	for _, child := range t.items {
		count += countLabeledNodes(child)
	}
	return count
}

func cloneLabeled(t *labeledTerm) *labeledTerm {
	if !t.isList {
		return &labeledTerm{name: &labeledSymbol{name: t.name.name, kind: t.name.kind, id: t.name.id}}
	}
	result := &labeledTerm{isList: true}
	for _, child := range t.items {
		result.items = append(result.items, cloneLabeled(child))
	}
	return result
}
