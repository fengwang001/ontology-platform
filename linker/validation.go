package linker

// validateDef 按 AddModule 的参数规则校验模块定义。
// 拒绝优先级：参数非法 > 名字已登记 > 模块数超限（后两者由调用方处理）。
func validateDef(def ModuleDef) (deps []string, imported map[string]string, localKinds map[string]BindingKind, err error) {
	if !validName(def.Name) {
		return nil, nil, nil, ErrInvalidArg
	}
	if len(def.Imports) > maxImports || len(def.Exports) > maxExports ||
		len(def.ReExports) > maxReExports || len(def.Body) > maxSteps {
		return nil, nil, nil, ErrInvalidArg
	}

	localKinds = map[string]BindingKind{}
	for _, e := range def.Exports {
		if !validName(e.Name) || (e.Kind != KindFunction && e.Kind != KindLet) {
			return nil, nil, nil, ErrInvalidArg
		}
		if _, dup := localKinds[e.Name]; dup {
			return nil, nil, nil, ErrInvalidArg
		}
		localKinds[e.Name] = e.Kind
	}

	named := map[string]bool{}
	deps = make([]string, 0, len(def.Imports)+len(def.ReExports))
	for _, r := range def.ReExports {
		if !validName(r.Source) || r.Source == def.Name {
			return nil, nil, nil, ErrInvalidArg
		}
		deps = append(deps, r.Source)
		if r.IsStar() {
			if r.From != "" {
				return nil, nil, nil, ErrInvalidArg
			}
			continue
		}
		if !validName(r.Name) || !validName(r.From) {
			return nil, nil, nil, ErrInvalidArg
		}
		if _, isLocal := localKinds[r.Name]; isLocal || named[r.Name] {
			return nil, nil, nil, ErrInvalidArg
		}
		named[r.Name] = true
	}

	imported = map[string]string{}
	for _, st := range def.Imports {
		if !validName(st.Source) || st.Source == def.Name {
			return nil, nil, nil, ErrInvalidArg
		}
		if len(st.Bindings) < 1 || len(st.Bindings) > maxBindings {
			return nil, nil, nil, ErrInvalidArg
		}
		deps = append(deps, st.Source)
		for _, b := range st.Bindings {
			if !validName(b) {
				return nil, nil, nil, ErrInvalidArg
			}
			if _, dup := imported[b]; dup {
				return nil, nil, nil, ErrInvalidArg
			}
			if _, isExport := localKinds[b]; isExport || named[b] {
				return nil, nil, nil, ErrInvalidArg
			}
			imported[b] = st.Source
		}
	}

	for _, step := range def.Body {
		switch step.Kind {
		case StepInit:
			if k, ok := localKinds[step.Name]; !ok || k != KindLet {
				return nil, nil, nil, ErrInvalidArg
			}
		case StepRead:
			if !validName(step.Module) {
				return nil, nil, nil, ErrInvalidArg
			}
			if step.Module == def.Name {
				if _, ok := localKinds[step.Name]; !ok {
					return nil, nil, nil, ErrInvalidArg
				}
			} else if src, ok := imported[step.Name]; !ok || src != step.Module {
				return nil, nil, nil, ErrInvalidArg
			}
		case StepThrow:
			if len(step.Message) < 1 || len(step.Message) > maxMessageLen {
				return nil, nil, nil, ErrInvalidArg
			}
		default:
			return nil, nil, nil, ErrInvalidArg
		}
	}
	return deps, imported, localKinds, nil
}
