package ontology

import "fmt"

// validate 只检查动作声明自身的合法性（固定错误优先级中的第 1 类），
// 不触碰任何实例、链接或时钟。
func validate(a ActionDeclaration) error {
	if a.Name == "" {
		return fmt.Errorf("invalid action declaration: empty name")
	}
	if a.Subject == "" {
		return fmt.Errorf("invalid action declaration: empty subject")
	}
	if len(a.Direct) == 0 {
		return fmt.Errorf("invalid action declaration: at least one direct operation is required")
	}
	if a.MaxDepth < 0 {
		return fmt.Errorf("invalid action declaration: negative max depth")
	}
	if a.InvisibleMode != RejectOnInvisible && a.InvisibleMode != SkipOnInvisible {
		return fmt.Errorf("invalid action declaration: unknown invisible mode")
	}
	if a.Merge != MergeAll && a.Merge != MergeAny {
		return fmt.Errorf("invalid action declaration: unknown merge policy")
	}

	seenTarget := map[InstanceID]bool{}
	for i, op := range a.Direct {
		switch op.Op {
		case OpCreate, OpRead, OpUpdate, OpDelete:
		default:
			return fmt.Errorf("invalid action declaration: unknown operation %q", op.Op)
		}
		if op.Target == "" {
			return fmt.Errorf("invalid action declaration: direct op %d has empty target", i)
		}
		if op.Type == "" {
			return fmt.Errorf("invalid action declaration: direct op %d has empty object type", i)
		}
		key := InstanceID(fmt.Sprintf("%s:%s", op.Op, op.Target))
		if seenTarget[key] {
			return fmt.Errorf("invalid action declaration: duplicate direct operation %s on %s", op.Op, op.Target)
		}
		seenTarget[key] = true
	}

	seenRule := map[LinkTypeID]bool{}
	for i, r := range a.Cascades {
		if r.LinkType == "" {
			return fmt.Errorf("invalid action declaration: cascade %d has empty link type", i)
		}
		switch r.Effect {
		case OpRead, OpUpdate, OpDelete:
		default:
			return fmt.Errorf("invalid action declaration: cascade %d has unsupported effect %q", i, r.Effect)
		}
		if seenRule[r.LinkType] {
			return fmt.Errorf("invalid action declaration: duplicate cascade rule for link type %s", r.LinkType)
		}
		seenRule[r.LinkType] = true
	}
	return nil
}
