package defassign

// 本文件定义检查子系统的输入程序结构（程序结构登记的一部分），
// 以及把文本 DSL 登记成结构树的解析器。
//
// DSL（按行）：
//   var x y z        声明变量（可出现多次）
//   assign x         赋值
//   use x            读取
//   if               条件分支（条件不定）
//   iftrue / iffalse 常量条件：恒真 / 恒假
//   loop             循环（至少执行零次）
//   loop1            至少执行一次的循环
//   break            跳出当前循环（先执行栈上所有清理区域）
//   return           提前返回（先执行栈上所有清理区域）
//   try              异常保护结构开始
//   handler          处理分支（仅可直接处于 try 与 cleanup 之间）
//   cleanup          清理区域开始
//   else             if 的另一侧
//   end              结束最近的 if / loop / try / cleanup

// Pos 是语句在程序中的出现位置（按登记顺序编号，从 1 起）。
type Pos int

// Kind 标识语句种类。
type Kind uint8

const (
	KDeclare Kind = iota
	KAssign
	KUse
	KIf
	KLoop
	KBreak
	KReturn
	KTry
	KHandler
	KCleanup
	KEnd
)

func (k Kind) String() string { return kindNames[k] }

var kindNames = [...]string{
	"declare", "assign", "use", "if", "loop", "break",
	"return", "try", "handler", "cleanup", "end",
}

// Stmt 是结构树中的一个节点。
type Stmt struct {
	Pos  Pos
	Kind Kind
	Var  string

	CondConst   int  // if: 0 不定；1 恒真；-1 恒假
	AtLeast1    bool // loop: 是否标注至少执行一次
	Body        []*Stmt
	Else        []*Stmt
	Handlers    [][]*Stmt
	Cleanup     []*Stmt
	HandlerPos  []Pos // try 的各处理分支位置
	HandlerLine []int

	Decls []string // declare 语句声明的变量
	Line  int
}

// Program 是一次检查的输入。
type Program struct {
	Name    string
	Vars    map[string]bool
	Stmts   []*Stmt
	src     string
	nextPos Pos
}

// Source 返回登记此程序所用的原始 DSL 文本。
func (p *Program) Source() string { return p.src }

// ParseError 描述登记阶段（词法/结构配对）的失败。
type ParseError struct {
	Line int
	Msg  string
}

func (e *ParseError) Error() string {
	return "parse error at line " + itoa(e.Line) + ": " + e.Msg
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

type frame struct {
	node *Stmt
	mode frameMode
}

type frameMode uint8

const (
	fmSeq frameMode = iota
	fmElse
	fmHandler
	fmCleanup
)

// Parse 把 DSL 文本登记为结构树。解析只负责词法与结构配对，
// 不做语义校验（未声明变量、break/handler 位置、环等由 Validate 负责）。
func Parse(name, src string) (*Program, error) {
	prog := &Program{Name: name, Vars: map[string]bool{}, src: src, nextPos: 1}
	var stack []frame
	top := &prog.Stmts

	emit := func(s *Stmt) {
		s.Pos = prog.nextPos
		prog.nextPos++
		*top = append(*top, s)
	}

	lines := splitLines(src)
	for lineNo, raw := range lines {
		ln := lineNo + 1
		line := trimSpace(raw)
		if line == "" || line[0] == '#' {
			continue
		}
		fields := fieldsN(line, 0)
		cmd := fields[0]
		need := func(n int) error {
			if len(fields)-1 != n {
				return &ParseError{ln, cmd + " expects " + itoa(n) + " operand(s)"}
			}
			return nil
		}
		switch cmd {
		case "var":
			if len(fields) < 2 {
				return nil, &ParseError{ln, "var requires at least one name"}
			}
			s := &Stmt{Kind: KDeclare, Line: ln, Decls: append([]string(nil), fields[1:]...)}
			emit(s)
			for _, v := range fields[1:] {
				prog.Vars[v] = true
			}
		case "assign":
			if err := need(1); err != nil {
				return nil, err
			}
			emit(&Stmt{Kind: KAssign, Var: fields[1], Line: ln})
		case "use":
			if err := need(1); err != nil {
				return nil, err
			}
			emit(&Stmt{Kind: KUse, Var: fields[1], Line: ln})
		case "if", "iftrue", "iffalse":
			if err := need(0); err != nil {
				return nil, err
			}
			s := &Stmt{Kind: KIf, Line: ln}
			if cmd == "iftrue" {
				s.CondConst = 1
			} else if cmd == "iffalse" {
				s.CondConst = -1
			}
			emit(s)
			stack = append(stack, frame{node: s, mode: fmSeq})
			top = &s.Body
		case "else":
			if err := need(0); err != nil {
				return nil, err
			}
			if len(stack) == 0 || stack[len(stack)-1].node.Kind != KIf || stack[len(stack)-1].mode != fmSeq {
				return nil, &ParseError{ln, "else without matching if"}
			}
			f := &stack[len(stack)-1]
			f.mode = fmElse
			top = &f.node.Else
		case "loop", "loop1":
			if err := need(0); err != nil {
				return nil, err
			}
			s := &Stmt{Kind: KLoop, AtLeast1: cmd == "loop1", Line: ln}
			emit(s)
			stack = append(stack, frame{node: s, mode: fmSeq})
			top = &s.Body
		case "break":
			if err := need(0); err != nil {
				return nil, err
			}
			emit(&Stmt{Kind: KBreak, Line: ln})
		case "return":
			if err := need(0); err != nil {
				return nil, err
			}
			emit(&Stmt{Kind: KReturn, Line: ln})
		case "try":
			if err := need(0); err != nil {
				return nil, err
			}
			s := &Stmt{Kind: KTry, Line: ln}
			emit(s)
			stack = append(stack, frame{node: s, mode: fmSeq})
			top = &s.Body
		case "handler":
			if err := need(0); err != nil {
				return nil, err
			}
			if len(stack) > 0 && stack[len(stack)-1].node.Kind == KTry {
				f := &stack[len(stack)-1]
				f.node.Handlers = append(f.node.Handlers, nil)
				f.node.HandlerPos = append(f.node.HandlerPos, prog.nextPos)
				f.node.HandlerLine = append(f.node.HandlerLine, ln)
				prog.nextPos++
				f.mode = fmHandler
				top = &f.node.Handlers[len(f.node.Handlers)-1]
			} else {
				// 语法允许登记；「不属于任何保护结构」由语义校验按拒绝次序判定。
				emit(&Stmt{Kind: KHandler, Line: ln})
			}
		case "cleanup":
			if err := need(0); err != nil {
				return nil, err
			}
			if len(stack) == 0 || stack[len(stack)-1].node.Kind != KTry {
				return nil, &ParseError{ln, "cleanup without matching try"}
			}
			f := &stack[len(stack)-1]
			f.mode = fmCleanup
			top = &f.node.Cleanup
		case "end":
			if err := need(0); err != nil {
				return nil, err
			}
			if len(stack) == 0 {
				return nil, &ParseError{ln, "end without open block"}
			}
			stack = stack[:len(stack)-1]
			top = currentTarget(&stack, &prog.Stmts)
		default:
			return nil, &ParseError{ln, "unknown command: " + cmd}
		}
	}
	if len(stack) != 0 {
		return nil, &ParseError{Line: len(lines) + 1, Msg: "unterminated " + stack[len(stack)-1].node.Kind.String()}
	}
	return prog, nil
}

func enclosingTry(stack *[]frame) (*frame, bool) {
	for i := len(*stack) - 1; i >= 0; i-- {
		if (*stack)[i].node.Kind == KTry {
			return &(*stack)[i], true
		}
	}
	return nil, false
}

func currentTarget(stack *[]frame, root *[]*Stmt) *[]*Stmt {
	if len(*stack) == 0 {
		return root
	}
	f := &(*stack)[len(*stack)-1]
	switch f.mode {
	case fmSeq:
		return &f.node.Body
	case fmElse:
		return &f.node.Else
	case fmHandler:
		return &f.node.Handlers[len(f.node.Handlers)-1]
	case fmCleanup:
		return &f.node.Cleanup
	}
	panic("unreachable")
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func trimSpace(s string) string {
	i, j := 0, len(s)
	for i < j && isSpace(s[i]) {
		i++
	}
	for j > i && isSpace(s[j-1]) {
		j--
	}
	return s[i:j]
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r'
}

func fieldsN(s string, limit int) []string {
	var out []string
	i := 0
	for i < len(s) {
		for i < len(s) && isSpace(s[i]) {
			i++
		}
		if i >= len(s) {
			break
		}
		start := i
		for i < len(s) && !isSpace(s[i]) {
			i++
		}
		out = append(out, s[start:i])
		if limit > 0 && len(out) == limit {
			break
		}
	}
	return out
}
