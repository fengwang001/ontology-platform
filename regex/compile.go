package regex

// 指令集（下标从 0 起）：
//
//	opChar/opAny/opClass 消耗一个字节；opAssertStart/opAssertEnd 为断言；
//	opSplit(x,y) 优先跳 x，把 (y,pos) 压栈；opJmp 无条件跳转；opMatch 成功。
type op int8

const (
	opChar op = iota
	opAny
	opClass
	opSplit
	opJmp
	opAssertStart
	opAssertEnd
	opMatch
)

type inst struct {
	op  op
	b   byte
	set *[256]bool
	x   int
	y   int
}

type compiler struct {
	prog  []inst
	limit int64
}

// compile 把 AST 编译为指令序列，末尾追加 1 条 Match；
// 指令总数超过 limit 时返回 ErrProgramTooLarge。
func compile(root *node, limit int64) ([]inst, error) {
	c := &compiler{limit: limit}
	if err := c.emitNode(root); err != nil {
		return nil, err
	}
	if err := c.emit(inst{op: opMatch}); err != nil {
		return nil, err
	}
	return c.prog, nil
}

func (c *compiler) emit(in inst) error {
	c.prog = append(c.prog, in)
	if int64(len(c.prog)) > c.limit {
		return ErrProgramTooLarge
	}
	return nil
}

func (c *compiler) emitNode(n *node) error {
	switch n.kind {
	case kChar:
		return c.emit(inst{op: opChar, b: n.b})
	case kAny:
		return c.emit(inst{op: opAny})
	case kClass:
		return c.emit(inst{op: opClass, set: n.set})
	case kAssertStart:
		return c.emit(inst{op: opAssertStart})
	case kAssertEnd:
		return c.emit(inst{op: opAssertEnd})
	case kConcat:
		for _, ch := range n.children {
			if err := c.emitNode(ch); err != nil {
				return err
			}
		}
	case kAlt:
		return c.emitAlt(n.children)
	case kRepeat:
		return c.emitRepeat(n)
	}
	return nil
}

// emitAlt：x|y 编译为 Split(下一条, y 的起点)、x、Jmp(出口)、y；多个分支向右嵌套。
func (c *compiler) emitAlt(branches []*node) error {
	if len(branches) == 1 {
		return c.emitNode(branches[0])
	}
	split := len(c.prog)
	if err := c.emit(inst{op: opSplit}); err != nil {
		return err
	}
	c.prog[split].x = len(c.prog)
	if err := c.emitNode(branches[0]); err != nil {
		return err
	}
	jmp := len(c.prog)
	if err := c.emit(inst{op: opJmp}); err != nil {
		return err
	}
	c.prog[split].y = len(c.prog)
	if err := c.emitAlt(branches[1:]); err != nil {
		return err
	}
	c.prog[jmp].x = len(c.prog)
	return nil
}

// emitRepeat：
//
//	e* / e+ / e{m,}：复制 min 份 e 后接 e*（e* 为 L0: Split(L1, 出口)、e、Jmp L0）
//	e? / e{m,n}：复制 m 份 e 后接嵌套的 n-m 个可选 (e(e(…)?)?)?，
//	             每个可选的 Split 在跳过时都直接去往整个构造的出口
//
// 懒惰把每个 Split 的两个目标的优先次序对调。
func (c *compiler) emitRepeat(n *node) error {
	for i := 0; i < n.min; i++ {
		if err := c.emitNode(n.child); err != nil {
			return err
		}
	}
	if n.max == -1 {
		return c.emitStar(n.child, n.lazy)
	}
	splits := make([]int, 0, n.max-n.min)
	for j := 0; j < n.max-n.min; j++ {
		splits = append(splits, len(c.prog))
		if err := c.emit(inst{op: opSplit}); err != nil {
			return err
		}
		c.prog[splits[j]].x = len(c.prog)
		if err := c.emitNode(n.child); err != nil {
			return err
		}
	}
	exit := len(c.prog)
	for _, s := range splits {
		if n.lazy {
			c.prog[s].x, c.prog[s].y = exit, c.prog[s].x
		} else {
			c.prog[s].y = exit
		}
	}
	return nil
}

func (c *compiler) emitStar(child *node, lazy bool) error {
	l0 := len(c.prog)
	if err := c.emit(inst{op: opSplit}); err != nil {
		return err
	}
	body := len(c.prog)
	if err := c.emitNode(child); err != nil {
		return err
	}
	if err := c.emit(inst{op: opJmp, x: l0}); err != nil {
		return err
	}
	exit := len(c.prog)
	if lazy {
		c.prog[l0].x, c.prog[l0].y = exit, body
	} else {
		c.prog[l0].x, c.prog[l0].y = body, exit
	}
	return nil
}

// execResult 为一次搜索的内部结果。
type execResult struct {
	matched bool
	limited bool // 达到有效上限 λ
	start   int
	end     int
	steps   int64
}

type frame struct {
	pc  int
	pos int
}

// search 在 input 上从起点 0..n 依次尝试，用显式栈做优先序回溯。
// 每次调度一条指令之前：记忆化开启时若 (pc,pos) 已被调度过则视为失败且不计步；
// 否则标记，若 steps 已等于 limit 则立即超限结束，否则 steps 加一并执行。
func search(prog []inst, input []byte, limit int64, memo bool) execResult {
	n := len(input)
	var steps int64
	var seen map[[2]int]struct{}
	if memo {
		seen = make(map[[2]int]struct{})
	}
	stack := make([]frame, 0, 64)
	for s := 0; s <= n; s++ {
		stack = stack[:0]
		pc, pos := 0, s
		var ins inst
		for {
			if memo {
				key := [2]int{pc, pos}
				if _, ok := seen[key]; ok {
					goto fail
				}
				seen[key] = struct{}{}
			}
			if steps == limit {
				return execResult{limited: true, steps: steps}
			}
			steps++
			ins = prog[pc]
			switch ins.op {
			case opChar:
				if pos < n && input[pos] == ins.b {
					pc++
					pos++
				} else {
					goto fail
				}
			case opAny:
				if pos < n && input[pos] != '\n' {
					pc++
					pos++
				} else {
					goto fail
				}
			case opClass:
				if pos < n && ins.set[input[pos]] {
					pc++
					pos++
				} else {
					goto fail
				}
			case opAssertStart:
				if pos == 0 {
					pc++
				} else {
					goto fail
				}
			case opAssertEnd:
				if pos == n {
					pc++
				} else {
					goto fail
				}
			case opSplit:
				stack = append(stack, frame{pc: ins.y, pos: pos})
				pc = ins.x
			case opJmp:
				pc = ins.x
			case opMatch:
				return execResult{matched: true, start: s, end: pos, steps: steps}
			}
			continue
		fail:
			if len(stack) == 0 {
				break
			}
			f := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			pc, pos = f.pc, f.pos
		}
	}
	return execResult{steps: steps}
}
