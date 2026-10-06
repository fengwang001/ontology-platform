package callstack

// tailpos.go 是纯粹的尾位置（语法位置）判定模块。
//
// 判定与被调函数是谁无关，只看调用在语法树中的位置：
//   - 函数体最后求值的调用是尾位置；
//   - 条件表达式两侧分支最后求值的调用是尾位置；
//   - 顺序表达式最后一项中的调用是尾位置（let 的 body 同理）；
//   - 作为参数的调用、结果还要参与运算的调用（二元运算、条件、
//     throw 参数、let 初值等）不是尾位置；
//   - 处于异常保护区域 (try body) 之内的调用一律不是尾位置，
//     即使它位于区域最末——区域出口尚待执行。
//
// 常量开销保证：标注（MarkTail）在程序载入时对函数体做一次
// O(函数体大小) 的遍历；运行时判定一次调用是否处于尾位置，只是读取
// CallExpr.Tail 这个布尔字段，为 O(1)，不再遍历函数体。

// Expr 是小型函数式语言的表达式节点。
type Expr interface{ exprNode() }

// NumExpr 是整数字面量。
type NumExpr struct{ Value int64 }

// VarExpr 是变量引用。Slot 是载入期解析好的帧内槽位下标。
type VarExpr struct {
	Name string
	Slot int
}

// CallExpr 是命名函数调用。
// Tail/TailReason 在 MarkTail 阶段一次性确定，运行时 O(1) 读取。
type CallExpr struct {
	Name       string
	Args       []Expr
	Tail       bool
	TailReason string
}

// IfExpr 是条件表达式：仅 Then/Else 可处于尾位置，Cond 永不是。
type IfExpr struct {
	Cond Expr
	Then Expr
	Else Expr
}

// BinExpr 是二元算术/比较运算：两个操作数的结果都要参与后续运算。
type BinExpr struct {
	Op  string
	Lhs Expr
	Rhs Expr
}

// SeqExpr 是顺序表达式：仅最后一项可处于尾位置。
type SeqExpr struct{ Exprs []Expr }

// LetExpr 是词法绑定：Init 不是尾位置，Body 可处于尾位置。
type LetExpr struct {
	Name string
	Slot int
	Init Expr
	Body Expr
}

// TryExpr 是异常保护：handler 绑定异常值。
// Body 与 Handler 内的所有调用都不是尾位置（尾屏障）。
type TryExpr struct {
	Body    Expr
	Handler Expr
	ExcSlot int
}

// ThrowExpr 抛出一个整数异常；Arg 不是尾位置。
type ThrowExpr struct{ Arg Expr }

func (*NumExpr) exprNode()   {}
func (*VarExpr) exprNode()   {}
func (*CallExpr) exprNode()  {}
func (*IfExpr) exprNode()    {}
func (*BinExpr) exprNode()   {}
func (*SeqExpr) exprNode()   {}
func (*LetExpr) exprNode()   {}
func (*TryExpr) exprNode()   {}
func (*ThrowExpr) exprNode() {}

// Func 是函数声明。NSlots 是参数槽与局部变量槽的总数。
type Func struct {
	Name   string
	Params []string
	NSlots int
	Body   Expr
}

// Program 是载入后的程序：一组命名函数加一个主表达式。
type Program struct {
	Funcs map[string]*Func
	Main  Expr
	// MainSlots 是主表达式的帧槽位数（其 let/catch 绑定）。
	MainSlots int
}

// 尾位置/屏障原因常量，既用于 CallExpr.TailReason，也用于日志判定依据。
const (
	reasonBody      = "last-expr-of-function-body"
	reasonIfBranch  = "last-expr-of-if-branch"
	reasonSeqLast   = "last-item-of-sequence"
	reasonLetBody   = "body-after-let"
	reasonArg       = "call-used-as-argument"
	reasonBin       = "result-feeds-binary-op"
	reasonIfCond    = "if-condition"
	reasonSeqItem   = "non-last-item-of-sequence"
	reasonLetInit   = "let-initializer"
	reasonProtected = "inside-protected-region"
	reasonHandler   = "inside-exception-handler"
	reasonThrowArg  = "throw-argument"
)

// annotate 以 e 所处的语法上下文（tail 与原因）递归标注全部调用节点。
func annotate(e Expr, tail bool, reason string) {
	switch n := e.(type) {
	case *NumExpr, *VarExpr:
	case *CallExpr:
		n.Tail = tail
		n.TailReason = reason
		for _, a := range n.Args {
			annotate(a, false, reasonArg)
		}
	case *IfExpr:
		annotate(n.Cond, false, reasonIfCond)
		// 两个分支继承同样的尾上下文；分支内最终调用即尾调用。
		brReason := reason
		if tail {
			brReason = reasonIfBranch
		}
		annotate(n.Then, tail, brReason)
		annotate(n.Else, tail, brReason)
	case *BinExpr:
		annotate(n.Lhs, false, reasonBin)
		annotate(n.Rhs, false, reasonBin)
	case *SeqExpr:
		for i, x := range n.Exprs {
			last := i == len(n.Exprs)-1
			if last {
				r := reason
				if tail {
					r = reasonSeqLast
				}
				annotate(x, tail, r)
			} else {
				annotate(x, false, reasonSeqItem)
			}
		}
	case *LetExpr:
		annotate(n.Init, false, reasonLetInit)
		r := reason
		if tail {
			r = reasonLetBody
		}
		annotate(n.Body, tail, r)
	case *TryExpr:
		// 整个 try 是尾屏障：无论外层是否尾位置，保护区域与处理器
		// 内的调用都不是尾调用。
		annotate(n.Body, false, reasonProtected)
		annotate(n.Handler, false, reasonHandler)
	case *ThrowExpr:
		annotate(n.Arg, false, reasonThrowArg)
	}
}

// MarkTail 在程序载入期标注每个函数体的尾位置。
// 主表达式不是函数体：它是程序入口边界，其调用视为非尾调用
// （入口帧 <main> 不被复用，回溯恒以它为根）。
// 标注完成后，运行时判定是对 CallExpr.Tail 的 O(1) 字段读取。
func MarkTail(p *Program) {
	for _, fn := range p.Funcs {
		annotate(fn.Body, true, reasonBody)
	}
	annotate(p.Main, false, reasonSeqItem)
}

// IsTailPosition 仅供测试/诊断使用：直接返回载入期的判定结果。
func IsTailPosition(c *CallExpr) bool { return c.Tail }
