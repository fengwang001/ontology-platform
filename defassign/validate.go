package defassign

// InputError 是输入校验失败。校验拒绝次序（由 Validate 严格保证）：
// 结构环 > 未声明变量 > 跳出位置非法 > 分支归属非法。
type InputErrorKind uint8

const (
	ErrStructureCycle InputErrorKind = iota + 1
	ErrUndeclaredVar
	ErrBreakOutsideLoop
	ErrHandlerOutsideTry
)

func (k InputErrorKind) String() string {
	switch k {
	case ErrStructureCycle:
		return "structure cycle"
	case ErrUndeclaredVar:
		return "undeclared variable"
	case ErrBreakOutsideLoop:
		return "break outside loop"
	case ErrHandlerOutsideTry:
		return "handler outside protect structure"
	}
	return "?"
}

// InputError 携带首个（按拒绝次序）输入错误的客观定位。
type InputError struct {
	Kind InputErrorKind
	At   Pos
	Line int
	Var  string
}

func (e *InputError) Error() string {
	s := e.Kind.String()
	if e.At > 0 {
		s += " at pos " + itoa(int(e.At)) + " (line " + itoa(e.Line) + ")"
	}
	if e.Var != "" {
		s += ": " + e.Var
	}
	return s
}

type cycleColor uint8

const (
	cycleWhite cycleColor = iota
	cycleGray
	cycleBlack
)

type cycleState struct {
	color  map[*Stmt]cycleColor
	parent map[*Stmt]*Stmt
}

// childGroups 返回一个结构节点的子结构分组，用于遍历与环检测。
func childGroups(s *Stmt) [][]*Stmt {
	switch s.Kind {
	case KIf:
		return [][]*Stmt{s.Body, s.Else}
	case KLoop:
		return [][]*Stmt{s.Body}
	case KTry:
		groups := make([][]*Stmt, 0, len(s.Handlers)+2)
		groups = append(groups, s.Body)
		groups = append(groups, s.Handlers...)
		groups = append(groups, s.Cleanup)
		return groups
	}
	return nil
}

// Validate 对登记后的程序做输入校验。任一类错误即整体拒绝，不产生部分诊断。
// 拒绝次序：结构环 > 未声明变量 > 跳出位置非法 > 分支归属非法。
func Validate(p *Program) error {
	st := &cycleState{
		color:  map[*Stmt]cycleColor{},
		parent: map[*Stmt]*Stmt{},
	}
	if err := detectCycle(st, p.Stmts); err != nil {
		return err
	}
	if err := checkVars(p, p.Stmts, nil); err != nil {
		return err
	}
	if err := checkBreak(p.Stmts, 0, nil); err != nil {
		return err
	}
	if err := checkHandler(p.Stmts, nil); err != nil {
		return err
	}
	return nil
}

// detectCycle 做三色 DFS。正常登记的结构树无环；手工构造的 Program
// 可能让某节点出现在自己的后代位置，此时在首次回边（Gray->Gray）处拒绝。
// 「按出现位置唯一」通过 DFS 先序访问保证：第一个被发现的回边即为答案。
func detectCycle(st *cycleState, stmts []*Stmt) error {
	for _, s := range stmts {
		if s == nil {
			continue
		}
		switch st.color[s] {
		case cycleGray:
			return &InputError{Kind: ErrStructureCycle, At: s.Pos, Line: s.Line}
		case cycleBlack:
			continue
		}
		st.color[s] = cycleGray
		for _, group := range childGroups(s) {
			if err := detectCycle(st, group); err != nil {
				return err
			}
		}
		st.color[s] = cycleBlack
	}
	return nil
}

func checkVars(p *Program, stmts []*Stmt, try *Stmt) error {
	for _, s := range stmts {
		if s == nil {
			continue
		}
		switch s.Kind {
		case KAssign, KUse:
			if !p.Vars[s.Var] {
				return &InputError{Kind: ErrUndeclaredVar, At: s.Pos, Line: s.Line, Var: s.Var}
			}
		case KTry:
			if err := checkVars(p, s.Body, s); err != nil {
				return err
			}
			for _, h := range s.Handlers {
				if err := checkVars(p, h, s); err != nil {
					return err
				}
			}
			if err := checkVars(p, s.Cleanup, s); err != nil {
				return err
			}
			continue
		case KIf:
			if err := checkVars(p, s.Body, try); err != nil {
				return err
			}
			if err := checkVars(p, s.Else, try); err != nil {
				return err
			}
			continue
		case KLoop:
			if err := checkVars(p, s.Body, try); err != nil {
				return err
			}
			continue
		}
	}
	return nil
}

func checkBreak(stmts []*Stmt, loops int, try *Stmt) error {
	for _, s := range stmts {
		if s == nil {
			continue
		}
		switch s.Kind {
		case KBreak:
			if loops == 0 {
				return &InputError{Kind: ErrBreakOutsideLoop, At: s.Pos, Line: s.Line}
			}
		case KLoop:
			if err := checkBreak(s.Body, loops+1, try); err != nil {
				return err
			}
		case KIf:
			if err := checkBreak(s.Body, loops, try); err != nil {
				return err
			}
			if err := checkBreak(s.Else, loops, try); err != nil {
				return err
			}
		case KTry:
			if err := checkBreak(s.Body, loops, s); err != nil {
				return err
			}
			for _, h := range s.Handlers {
				if err := checkBreak(h, loops, s); err != nil {
					return err
				}
			}
			if err := checkBreak(s.Cleanup, loops, s); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkHandler 要求 KHandler 节点只能作为 try 的直接分支内容出现。
// try 的分支容器本身不落在语句树里（位置记录在 try 上），因此
// 遍历任何序列时携带其所属 try；孤儿节点按其自身位置拒绝。
func checkHandler(stmts []*Stmt, try *Stmt) error {
	for _, s := range stmts {
		if s == nil {
			continue
		}
		switch s.Kind {
		case KHandler:
			return &InputError{Kind: ErrHandlerOutsideTry, At: s.Pos, Line: s.Line}
		case KTry:
			if err := checkHandler(s.Body, s); err != nil {
				return err
			}
			for _, h := range s.Handlers {
				if err := checkHandler(h, s); err != nil {
					return err
				}
			}
			if err := checkHandler(s.Cleanup, s); err != nil {
				return err
			}
		case KIf:
			if err := checkHandler(s.Body, try); err != nil {
				return err
			}
			if err := checkHandler(s.Else, try); err != nil {
				return err
			}
		case KLoop:
			if err := checkHandler(s.Body, try); err != nil {
				return err
			}
		}
	}
	return nil
}
