package match

func validatePattern(pattern Pattern, expectedType string, registry *Checker) error {
	switch p := pattern.(type) {
	case nil:
		return ErrInvalidArgument
	case Wildcard:
		return nil
	case ConstructorPattern:
		owner, ok := registry.constructors[p.Name]
		if !ok {
			return ErrUnknownConstructor
		}
		if owner.name != expectedType {
			return ErrWrongConstructorType
		}
		declaration := owner.constructor[p.Name]
		if len(p.Children) != len(declaration.FieldTypes) {
			return ErrConstructorArity
		}
		for i, child := range p.Children {
			if err := validatePattern(child, declaration.FieldTypes[i], registry); err != nil {
				return err
			}
		}
		return nil
	case OrPattern:
		if len(p.Branches) == 0 || len(p.Branches) > maxOrBranches {
			return ErrInvalidArgument
		}
		for _, branch := range p.Branches {
			if err := validatePattern(branch, expectedType, registry); err != nil {
				return err
			}
		}
		return nil
	default:
		return ErrInvalidArgument
	}
}

func patternDepth(pattern Pattern) int {
	switch p := pattern.(type) {
	case nil, Wildcard:
		return 0
	case ConstructorPattern:
		childDepth := 0
		for _, child := range p.Children {
			if depth := patternDepth(child); depth > childDepth {
				childDepth = depth
			}
		}
		return 1 + childDepth
	case OrPattern:
		branchDepth := 0
		for _, branch := range p.Branches {
			if depth := patternDepth(branch); depth > branchDepth {
				branchDepth = depth
			}
		}
		return 1 + branchDepth
	default:
		return maxPatternDepth + 1
	}
}

func shapeValid(pattern Pattern) bool {
	switch p := pattern.(type) {
	case nil:
		return false
	case Wildcard:
		return true
	case ConstructorPattern:
		for _, child := range p.Children {
			if !shapeValid(child) {
				return false
			}
		}
		return true
	case OrPattern:
		if len(p.Branches) == 0 || len(p.Branches) > maxOrBranches {
			return false
		}
		for _, branch := range p.Branches {
			if !shapeValid(branch) {
				return false
			}
		}
		return true
	default:
		return false
	}
}
