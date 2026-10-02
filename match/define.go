package match

func (c *Checker) DefineType(name string, constructors []Constructor) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(name) == 0 || len(name) > maxNameBytes ||
		len(constructors) == 0 || len(constructors) > maxConstructors ||
		len(c.types) >= maxTypes {
		return ErrInvalidArgument
	}
	for _, constructor := range constructors {
		if len(constructor.Name) == 0 || len(constructor.Name) > maxNameBytes ||
			len(constructor.FieldTypes) > maxFields {
			return ErrInvalidArgument
		}
		for _, fieldType := range constructor.FieldTypes {
			if len(fieldType) == 0 || len(fieldType) > maxNameBytes {
				return ErrInvalidArgument
			}
			if fieldType != name {
				if _, ok := c.types[fieldType]; !ok {
					return ErrInvalidArgument
				}
			}
		}
	}

	finite := false
	for _, constructor := range constructors {
		hasSelf := false
		for _, fieldType := range constructor.FieldTypes {
			if fieldType == name {
				hasSelf = true
			}
		}
		if !hasSelf {
			finite = true
			break
		}
	}
	if !finite {
		return ErrInvalidArgument
	}

	if _, exists := c.types[name]; exists {
		return ErrDuplicateName
	}
	seenConstructors := make(map[string]bool, len(constructors))
	for _, constructor := range constructors {
		if seenConstructors[constructor.Name] || c.constructors[constructor.Name] != nil {
			return ErrDuplicateName
		}
		seenConstructors[constructor.Name] = true
	}

	defined := &datatype{
		name:         name,
		constructors: make([]*Constructor, 0, len(constructors)),
		constructor:  make(map[string]*Constructor),
	}
	for i := range constructors {
		constructor := constructors[i]
		fields := append([]string(nil), constructor.FieldTypes...)
		declaration := &Constructor{Name: constructor.Name, FieldTypes: fields}
		defined.constructors = append(defined.constructors, declaration)
		defined.constructor[constructor.Name] = declaration
	}
	c.types[name] = defined
	for _, declaration := range defined.constructors {
		c.constructors[declaration.Name] = defined
	}
	return nil
}
