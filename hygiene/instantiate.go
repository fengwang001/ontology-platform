package hygiene

type introducedEntry struct {
	name    string
	binding *binding
}

func instantiateTemplate(def macro, args []*node, depth int) *node {
	paramArgs := make(map[string]*node, len(def.params))
	for i, name := range def.params {
		paramArgs[name] = args[i]
	}
	return instantiateNode(def.template, paramArgs, depth, nil, false)
}

func instantiateNode(n *node, args map[string]*node, depth int, scope []introducedEntry, quoted bool) *node {
	copyNode := cloneNode(n)
	setDepth(copyNode, depth)

	if n.atom {
		if arg, ok := args[n.text]; ok {
			result := cloneNode(arg)
			setDepth(result, depth)
			if quoted {
				markLiteral(result)
			}
			result.isBinder = n.isBinder
			return result
		}
		if quoted {
			copyNode.source = sourceLiteral
			copyNode.binding = nil
			return copyNode
		}
		if entry := lookupIntroduced(n.text, scope); entry != nil {
			copyNode.source = sourceBound
			copyNode.binding = entry.binding
			return copyNode
		}
		copyNode.source = sourceGlobal
		copyNode.binding = nil
		return copyNode
	}

	if !quoted && isLamShape(n) {
		lamCopy := copyNode
		binderScope := append([]introducedEntry(nil), scope...)
		for i, binder := range n.items[1].items {
			if binder.atom {
				if _, isParam := args[binder.text]; isParam {
					continue
				}
				bind := &binding{introduced: true}
				binderCopy := lamCopy.items[1].items[i]
				binderCopy.source = sourceBound
				binderCopy.binding = bind
				binderCopy.isBinder = true
				binderScope = append(binderScope, introducedEntry{name: binder.text, binding: bind})
			}
		}
		lamCopy.items[0] = instantiateNode(n.items[0], args, depth, scope, false)
		for i, binder := range n.items[1].items {
			if _, isParam := args[binder.text]; isParam && binder.atom {
				replaced := instantiateNode(binder, args, depth, binderScope, false)
				replaced.isBinder = true
				lamCopy.items[1].items[i] = replaced
			}
		}
		lamCopy.items[2] = instantiateNode(n.items[2], args, depth, binderScope, false)
		return lamCopy
	}

	if !quoted && len(n.items) >= 1 && n.items[0].atom && n.items[0].text == "quote" {
		for i, child := range n.items {
			if i == 0 {
				headCopy := cloneNode(child)
				setDepth(headCopy, depth)
				headCopy.source = sourceLiteral
				copyNode.items[i] = headCopy
			} else {
				copyNode.items[i] = instantiateNode(child, args, depth, scope, true)
			}
		}
		return copyNode
	}

	for i, child := range n.items {
		copyNode.items[i] = instantiateNode(child, args, depth, scope, quoted)
	}
	return copyNode
}

func lookupIntroduced(name string, scope []introducedEntry) *introducedEntry {
	for i := len(scope) - 1; i >= 0; i-- {
		if scope[i].name == name {
			return &scope[i]
		}
	}
	return nil
}

func isLamShape(n *node) bool {
	return len(n.items) == 3 && n.items[0].atom && n.items[0].text == "lam" && isList(n.items[1]) && len(n.items[1].items) > 0
}

func setDepth(n *node, depth int) {
	if n == nil {
		return
	}
	n.depth = depth
	for _, child := range n.items {
		setDepth(child, depth)
	}
}

func markLiteral(n *node) {
	if n == nil {
		return
	}
	if n.atom {
		n.source = sourceLiteral
		n.binding = nil
	}
	for _, child := range n.items {
		markLiteral(child)
	}
}
