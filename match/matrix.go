package match

func (c *Checker) missing(rows [][]Pattern, typeNames []string) ([]Pattern, bool) {
	c.missingCalls++
	if len(typeNames) == 0 {
		if len(rows) == 0 {
			return nil, true
		}
		return nil, false
	}

	rootType := c.types[typeNames[0]]
	sigma := firstColumnConstructors(rows)
	if len(sigma) == len(rootType.constructors) {
		for _, declaration := range rootType.constructors {
			specialized := specializeRows(rows, declaration)
			nextTypes := append(append([]string(nil), declaration.FieldTypes...), typeNames[1:]...)
			if witness, ok := c.missing(specialized, nextTypes); ok {
				fieldCount := len(declaration.FieldTypes)
				root := ConstructorPattern{
					Name:     declaration.Name,
					Children: append([]Pattern(nil), witness[:fieldCount]...),
				}
				return append([]Pattern{root}, witness[fieldCount:]...), true
			}
		}
		return nil, false
	}

	defaultRows := defaultRows(rows)
	witness, ok := c.missing(defaultRows, typeNames[1:])
	if !ok {
		return nil, false
	}
	var root Pattern
	if len(sigma) == 0 {
		root = Wildcard{}
	} else {
		var missingDeclaration *Constructor
		for _, declaration := range rootType.constructors {
			if !sigma[declaration.Name] {
				missingDeclaration = declaration
				break
			}
		}
		children := make([]Pattern, len(missingDeclaration.FieldTypes))
		for i := range children {
			children[i] = Wildcard{}
		}
		root = ConstructorPattern{Name: missingDeclaration.Name, Children: children}
	}
	return append([]Pattern{root}, witness...), true
}

func firstColumnConstructors(rows [][]Pattern) map[string]bool {
	constructors := make(map[string]bool)
	for _, row := range rows {
		if len(row) == 0 {
			continue
		}
		if constructor, ok := row[0].(ConstructorPattern); ok {
			constructors[constructor.Name] = true
		}
	}
	return constructors
}

func specializeRows(rows [][]Pattern, declaration *Constructor) [][]Pattern {
	specialized := make([][]Pattern, 0, len(rows))
	for _, row := range rows {
		switch head := row[0].(type) {
		case Wildcard:
			next := make([]Pattern, 0, len(declaration.FieldTypes)+len(row)-1)
			for range declaration.FieldTypes {
				next = append(next, Wildcard{})
			}
			next = append(next, row[1:]...)
			specialized = append(specialized, next)
		case ConstructorPattern:
			if head.Name == declaration.Name {
				next := make([]Pattern, 0, len(head.Children)+len(row)-1)
				next = append(next, head.Children...)
				next = append(next, row[1:]...)
				specialized = append(specialized, next)
			}
		}
	}
	return specialized
}

func defaultRows(rows [][]Pattern) [][]Pattern {
	defaults := make([][]Pattern, 0, len(rows))
	for _, row := range rows {
		if _, ok := row[0].(Wildcard); ok {
			defaults = append(defaults, append([]Pattern(nil), row[1:]...))
		}
	}
	return defaults
}
