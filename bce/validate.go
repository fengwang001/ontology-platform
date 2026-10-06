package bce

import "fmt"

// ErrorKind classifies input errors. Declaration order is the rejection
// order: undefined reference > multiple terminators > unreachable block >
// loop with multiple entries.
type ErrorKind int

const (
	ErrUndefinedRef ErrorKind = iota
	ErrMultiTerm
	ErrUnreachable
	ErrLoopMultiEntry
)

// Error is an invalid-input rejection. Invalid input yields no partial
// result: Analyze returns only the error.
type Error struct {
	Kind   ErrorKind
	Detail string
}

func (e *Error) Error() string { return e.Detail }

// Validate registers code and control flow and rejects invalid input.
// On success it assigns check IDs in block declaration order.
func Validate(p *Program) error {
	if err := checkUndefined(p); err != nil {
		return err
	}
	if err := checkMultiTerm(p); err != nil {
		return err
	}
	if err := checkUnreachable(p); err != nil {
		return err
	}
	if err := checkLoopEntries(p); err != nil {
		return err
	}
	id := 0
	for _, b := range p.Blocks {
		for i := range b.Instrs {
			if b.Instrs[i].Kind == OpCheck {
				b.Instrs[i].ID = id
				id++
			}
		}
	}
	return nil
}

func checkUndefined(p *Program) error {
	vars := map[string]bool{}
	for _, v := range p.Vars {
		vars[v] = true
	}
	arrays := map[string]bool{}
	for _, a := range p.Arrays {
		arrays[a] = true
	}
	blocks := map[string]bool{}
	for _, b := range p.Blocks {
		if blocks[b.Name] {
			return &Error{ErrUndefinedRef, fmt.Sprintf("duplicate block %q", b.Name)}
		}
		blocks[b.Name] = true
	}
	if !blocks[p.Entry] {
		return &Error{ErrUndefinedRef, fmt.Sprintf("entry block %q undefined", p.Entry)}
	}
	checkOp := func(o Operand) error {
		if !o.IsConst && !vars[o.Var] {
			return &Error{ErrUndefinedRef, fmt.Sprintf("undefined variable %q", o.Var)}
		}
		return nil
	}
	for _, b := range p.Blocks {
		for _, in := range b.Instrs {
			switch in.Kind {
			case OpAssignConst:
				if !vars[in.Dst] {
					return &Error{ErrUndefinedRef, fmt.Sprintf("undefined variable %q", in.Dst)}
				}
			case OpAssign, OpAssignAdd:
				for _, v := range []string{in.Dst, in.Src} {
					if !vars[v] {
						return &Error{ErrUndefinedRef, fmt.Sprintf("undefined variable %q", v)}
					}
				}
			case OpNewArray:
				if !arrays[in.Arr] {
					return &Error{ErrUndefinedRef, fmt.Sprintf("undefined array %q", in.Arr)}
				}
			case OpAssignArray:
				if !arrays[in.Arr] || !arrays[in.Src] {
					return &Error{ErrUndefinedRef, fmt.Sprintf("undefined array in %q = %q", in.Arr, in.Src)}
				}
			case OpCheck:
				if !arrays[in.Arr] {
					return &Error{ErrUndefinedRef, fmt.Sprintf("undefined array %q", in.Arr)}
				}
				if err := checkOp(in.Idx); err != nil {
					return err
				}
			}
		}
		for _, t := range b.Terms {
			switch t.Kind {
			case TJump:
				if !blocks[t.Target] {
					return &Error{ErrUndefinedRef, fmt.Sprintf("undefined block %q", t.Target)}
				}
			case TBranch:
				if err := checkOp(t.Left); err != nil {
					return err
				}
				if err := checkOp(t.Right); err != nil {
					return err
				}
				for _, s := range []string{t.Then, t.Else} {
					if !blocks[s] {
						return &Error{ErrUndefinedRef, fmt.Sprintf("undefined block %q", s)}
					}
				}
			}
		}
	}
	return nil
}

func checkMultiTerm(p *Program) error {
	for _, b := range p.Blocks {
		if len(b.Terms) != 1 {
			return &Error{ErrMultiTerm, fmt.Sprintf("block %q has %d terminators, want 1", b.Name, len(b.Terms))}
		}
	}
	return nil
}

// successors returns the successor block names of a terminator.
func successors(t Term) []string {
	switch t.Kind {
	case TJump:
		return []string{t.Target}
	case TBranch:
		if t.Then == t.Else {
			return []string{t.Then}
		}
		return []string{t.Then, t.Else}
	}
	return nil
}

func checkUnreachable(p *Program) error {
	reach := map[string]bool{p.Entry: true}
	queue := []string{p.Entry}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		for _, s := range successors(p.Block(n).Terms[0]) {
			if !reach[s] {
				reach[s] = true
				queue = append(queue, s)
			}
		}
	}
	for _, b := range p.Blocks {
		if !reach[b.Name] {
			return &Error{ErrUnreachable, fmt.Sprintf("block %q unreachable from entry", b.Name)}
		}
	}
	return nil
}

func checkLoopEntries(p *Program) error {
	for _, lp := range findLoops(p) {
		if len(lp.Entries) != 1 {
			return &Error{ErrLoopMultiEntry, fmt.Sprintf("loop has %d entries, want 1", len(lp.Entries))}
		}
	}
	return nil
}
