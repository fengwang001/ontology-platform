package match

func (c *Checker) useful(rows [][]Pattern, query []Pattern, typeNames []string) bool {
	if len(typeNames) == 0 {
		return len(rows) == 0
	}

	rootType := c.types[typeNames[0]]
	switch head := query[0].(type) {
	case Wildcard:
		sigma := firstColumnConstructors(rows)
		if len(sigma) < len(rootType.constructors) {
			return c.useful(defaultRows(rows), query[1:], typeNames[1:])
		}
		for _, declaration := range rootType.constructors {
			nextQuery := make([]Pattern, 0, len(declaration.FieldTypes)+len(query)-1)
			for range declaration.FieldTypes {
				nextQuery = append(nextQuery, Wildcard{})
			}
			nextQuery = append(nextQuery, query[1:]...)
			nextTypes := append(append([]string(nil), declaration.FieldTypes...), typeNames[1:]...)
			if c.useful(specializeRows(rows, declaration), nextQuery, nextTypes) {
				return true
			}
		}
		return false
	case ConstructorPattern:
		declaration := rootType.constructor[head.Name]
		nextQuery := append(append([]Pattern(nil), head.Children...), query[1:]...)
		nextTypes := append(append([]string(nil), declaration.FieldTypes...), typeNames[1:]...)
		return c.useful(specializeRows(rows, declaration), nextQuery, nextTypes)
	default:
		return false
	}
}
