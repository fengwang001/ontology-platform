package ontology

// Update 原子地把若干字段改成新值并生成下一个版本。
//
// 规则：
//   - changes 中每一项都必须通过校验（字段已声明、类型匹配、整数在界内），
//     任意一项失败则整次更新不生效、版本号不前进；
//   - 每个被写入的字段（即使新值与旧值相同）来源版本都前进到新版本号，
//     未出现在 changes 中的字段来源版本保持不变；
//   - 版本号严格递增，并发更新也不会重复。
func (m *Manager) Update(changes Fields) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if errs := m.validate(changes); len(errs) > 0 {
		return 0, errs
	}

	prev := m.versions[m.current]
	nextVersion := m.counter + 1
	values := make(Fields, len(prev.values))
	sources := make(map[string]int64, len(prev.sources))
	for name, v := range prev.values {
		values[name] = v
	}
	for name, src := range prev.sources {
		sources[name] = src
	}
	for name, v := range changes {
		values[name] = v
		sources[name] = nextVersion
	}

	m.counter = nextVersion
	m.versions[nextVersion] = &versionData{values: values, sources: sources}
	m.current = nextVersion
	return nextVersion, nil
}

func (m *Manager) validate(changes Fields) ValidationErrors {
	var errs ValidationErrors
	for name, v := range changes {
		decl, ok := m.decls[name]
		if !ok {
			errs = append(errs, &ValidationError{
				Field:  name,
				Kind:   FailureUnknownField,
				Detail: "field is not declared",
			})
			continue
		}
		if v.kind != decl.Kind {
			errs = append(errs, &ValidationError{
				Field:  name,
				Kind:   FailureTypeMismatch,
				Detail: "value type does not match declaration",
			})
			continue
		}
		if decl.Kind == KindInt && (v.num < decl.Min || v.num > decl.Max) {
			errs = append(errs, &ValidationError{
				Field:  name,
				Kind:   FailureOutOfRange,
				Detail: "integer value outside declared bounds",
			})
		}
	}
	return errs
}
