// Package register 实现单寄存器历史的线性一致性检查器。
//
// 寄存器初值为 0，支持写、读、比较交换（CAS）三种操作。
// 并发客户端通过 Begin/End 上报操作的调用与返回记录，
// Check 判定当前历史是否存在同时满足实时先后约束与
// 寄存器语义的全序，并在可行时给出唯一确定的见证序。
package register

import (
	"fmt"
	"sync"
)

// MaxOps 是检查器接受的操作总数上限。
const MaxOps = 20

// Kind 表示操作类型。
type Kind int

const (
	// Write 写操作，将寄存器置为 V。
	Write Kind = iota
	// Read 读操作，返回寄存器当前值。
	Read
	// CAS 比较交换：当前值等于 E 时成功并置为 N，否则失败且不变。
	CAS
)

// Operation 描述一次寄存器操作。
type Operation struct {
	Kind Kind
	V    int // Write：写入的值
	E    int // CAS：期望值
	N    int // CAS：成功后写入的新值
}

// NewWrite 构造写操作。
func NewWrite(v int) Operation { return Operation{Kind: Write, V: v} }

// NewRead 构造读操作。
func NewRead() Operation { return Operation{Kind: Read} }

// NewCAS 构造比较交换操作。
func NewCAS(e, n int) Operation { return Operation{Kind: CAS, E: e, N: n} }

// Result 描述一次已结束操作的结果。
// 写操作不得携带任何结果；读操作必须携带 Value；
// CAS 必须携带 Success。
type Result struct {
	Value   *int  // Read：读到的值
	Success *bool // CAS：是否成功
}

// WriteResult 构造写操作的结果（空结果）。
func WriteResult() Result { return Result{} }

// ReadResult 构造读操作的结果。
func ReadResult(v int) Result { return Result{Value: &v} }

// CASResult 构造 CAS 操作的结果。
func CASResult(ok bool) Result { return Result{Success: &ok} }

// opRecord 是检查器内部保存的单条操作记录。
type opRecord struct {
	id       int
	client   string
	op       Operation
	invoke   int
	finished bool
	ret      int
	res      Result
}

// Checker 收集并发客户端的调用/返回记录并判定线性一致性。
// 所有方法均可被并发调用。
type Checker struct {
	mu         sync.Mutex
	ops        []*opRecord
	pending    map[string]int // client -> 未结束操作编号
	lastFinish map[string]int // client -> 上次结束时刻
}

// NewChecker 创建一个空的检查器。
func NewChecker() *Checker {
	return &Checker{
		pending:    make(map[string]int),
		lastFinish: make(map[string]int),
	}
}

// Begin 记录一次操作调用，返回操作编号。
// 校验按顺序进行，只报告第一个错误，被拒绝时不改变任何状态：
//  1. 该客户端已有未结束操作；
//  2. 开始时刻小于该客户端上次结束时刻；
//  3. 操作总数已达上限。
func (c *Checker) Begin(client string, op Operation, invoke int) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, busy := c.pending[client]; busy {
		return -1, fmt.Errorf("register: client %q already has an unfinished operation", client)
	}
	if last, ok := c.lastFinish[client]; ok && invoke < last {
		return -1, fmt.Errorf("register: client %q invoke time %d is before its last finish time %d", client, invoke, last)
	}
	if len(c.ops) >= MaxOps {
		return -1, fmt.Errorf("register: operation count exceeds %d", MaxOps)
	}
	id := len(c.ops)
	c.ops = append(c.ops, &opRecord{id: id, client: client, op: op, invoke: invoke})
	c.pending[client] = id
	return id, nil
}

// End 记录一次操作返回。
// 校验按顺序进行，只报告第一个错误，被拒绝时不改变任何状态：
//  1. 编号不存在或已结束；
//  2. 结束时刻小于开始时刻；
//  3. 结果类型与操作不符。
func (c *Checker) End(id, ret int, res Result) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if id < 0 || id >= len(c.ops) || c.ops[id].finished {
		return fmt.Errorf("register: operation %d does not exist or is already finished", id)
	}
	rec := c.ops[id]
	if ret < rec.invoke {
		return fmt.Errorf("register: operation %d return time %d is before invoke time %d", id, ret, rec.invoke)
	}
	if err := checkResult(rec.op, res); err != nil {
		return err
	}
	rec.finished = true
	rec.ret = ret
	rec.res = res
	delete(c.pending, rec.client)
	c.lastFinish[rec.client] = ret
	return nil
}

// checkResult 校验结果类型与操作类型是否匹配。
func checkResult(op Operation, res Result) error {
	switch op.Kind {
	case Write:
		if res.Value != nil || res.Success != nil {
			return fmt.Errorf("register: write operation must not carry a result")
		}
	case Read:
		if res.Value == nil {
			return fmt.Errorf("register: read operation must carry a returned value")
		}
		if res.Success != nil {
			return fmt.Errorf("register: read operation must not carry a success flag")
		}
	case CAS:
		if res.Success == nil {
			return fmt.Errorf("register: cas operation must carry a success flag")
		}
		if res.Value != nil {
			return fmt.Errorf("register: cas operation must not carry a returned value")
		}
	}
	return nil
}

// Check 基于调用瞬间的一致快照判定历史是否可线性化。
// 返回判定结论与见证序（操作编号序列）；不可线性化时见证序为 nil。
// 相同历史反复调用得到相同结论与相同见证序。
func (c *Checker) Check() (bool, []int) {
	c.mu.Lock()
	snap := make([]opRecord, len(c.ops))
	for i, rec := range c.ops {
		snap[i] = *rec
	}
	c.mu.Unlock()
	return search(snap)
}
