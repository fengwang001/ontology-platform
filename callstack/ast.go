// Package callstack 实现小型函数式语言运行时的调用栈管理子系统。
//
// 职责拆分：
//   - 尾位置判定（tailpos.go）：登记期一次性的纯语法标注，运行期 O(1)；
//   - 帧管理（frame.go / runtime.go）：压帧、尾调用复用、栈深上限、
//     帧内存配额与不可分的复用事务；
//   - 回溯与诊断（frame.go / errors.go）：含“此帧前被尾调用省略帧数”
//     的回溯、未处理异常的传播路径、一致的统计快照；
//   - 配额与错误（errors.go / runtime.go）：未定义、参数不匹配、
//     深度超限、配额超限、未处理异常、实例已关闭，优先级固定可区分。
//
// 尾位置由语法决定，与被调函数无关；尾调用复用当前帧且不加深栈。
// Stats 与 Backtrace 可被其他执行流并发读取，结果等价于把读取插入
// 某个调用边界所得。典型入口为 New + Interpreter.Call。
package callstack

// Expr 是小型函数式语言的表达式节点。所有节点在函数登记时会被
// markTail 做一次尾位置预标注；运行期判定一次调用是否为尾位置只需
// 读取 Call.Tail 字段，因此是 O(1)，与函数体大小无关。
type Expr interface{ exprNode() }

// Int 为整数字面量。
type Int struct{ Value int64 }

// Var 引用当前帧的某个槽位。Index 由 NewFunction 按作用域一次性解析：
// 参数槽位为 0..len(params)-1，其余为 let/捕获绑定分配的局部槽位。
type Var struct {
	Name  string
	Index int
}

// Add 表示二元原始运算；两侧参数都需要先求值，因此参数位置不是尾位置。
type Add struct{ Left, Right Expr }

// Call 为函数调用。Args 中的调用位于参数位置，不是尾调用。
type Call struct {
	Name string
	Args []Expr
	Tail bool
}

// If 的两个分支各自最后求值的调用位于尾位置。
type If struct {
	Cond, Then, Else Expr
}

// Let 绑定一个局部变量；绑定表达式不是尾位置，Body 为尾位置上下文。
// LocalIndex 在函数登记时分配。
type Let struct {
	Name       string
	LocalIndex int
	Bound      Expr
	Body       Expr
}

// Seq 顺序求值；最后一项处于尾位置，其余项不是。
type Seq struct{ Items []Expr }

// Throw 抛出异常（携带一个整数值作为异常负载）。
type Throw struct{ Payload Expr }

// Try 为异常保护区域。受保护区内的任何调用（包括区域最末的调用）
// 都不是尾位置：区域出口（可能执行处理分支）尚待执行。
// Handler 通过 CaughtName 绑定捕获到的异常负载，槽位在登记时分配。
type Try struct {
	Protected   Expr
	CaughtName  string
	CaughtIndex int
	Handler     Expr
}

// Function 为函数声明。Slots 在登记时按“参数个数 + 局部绑定个数”
// 静态计算，作为该帧的内存配额占用。
type Function struct {
	Name   string
	Params []string
	Body   Expr
	Slots  int

	localIndex map[string]int
}

// NewFunction 解析参数与局部绑定的槽位、计算帧大小、预标注尾位置。
// 对同一函数体只执行一次，开销在登记期一次性支付。
func NewFunction(name string, params []string, body Expr) *Function {
	fn := &Function{
		Name:       name,
		Params:     append([]string(nil), params...),
		Body:       body,
		localIndex: map[string]int{},
	}
	fn.Slots = countLocals(fn, body) + len(params)
	markTail(body, true)
	return fn
}

func (*Int) exprNode()   {}
func (*Var) exprNode()   {}
func (*Add) exprNode()   {}
func (*Call) exprNode()  {}
func (*If) exprNode()    {}
func (*Let) exprNode()   {}
func (*Seq) exprNode()   {}
func (*Throw) exprNode() {}
func (*Try) exprNode()   {}
