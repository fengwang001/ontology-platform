package compensate

import "sort"

// NaiveModel 是独立实现的朴素「一次性消费」参照模型。
//
// 它对真实系统做了最强的简化假设，因此不存在重复投递、崩溃与并发：
//   - 单线程按操作的给定顺序串行执行；
//   - 每条「投递」操作要么是新事件（立即领取并顺序施加全部副作用），
//     要么显式标记为前一事件的重复投递（跳过），要么是独立等价调用
//     （携带新 EventID，正常处理）；
//   - 撤销操作只在对应事件「尚未被处理」时生效，否则补偿继续；
//   - 不发生故障，因此 E2/E4/E1 不适用（差分测试只覆盖正常路径）。
//
// 对照方式：把同一段随机操作序列分别喂给真实处理器（允许并发/重投）
// 与本模型（每条事件的首次/重复/独立身份由测试生成器显式声明），
// 最终逐项比较全部业务键与副作用标记键；真实结果还必须能在审计 Seq 全序中
// 找到一个串行顺序与之一致。
type NaiveModel struct {
	seen   map[string]bool
	undone map[string]bool
	kvs    map[string][]byte
	regs   *Registry
}

// NewNaiveModel 构造参照模型。
func NewNaiveModel(regs *Registry) *NaiveModel {
	return &NaiveModel{
		seen:   map[string]bool{},
		undone: map[string]bool{},
		kvs:    map[string][]byte{},
		regs:   regs,
	}
}

// Undo 在参照模型上记录撤销：仅当事件尚未处理时生效。
func (m *NaiveModel) Undo(ev Event) UndoOutcome {
	if m.seen[ev.EventID] {
		return UndoTooLate
	}
	m.undone[ev.EventID] = true
	return UndoWins
}

// Deliver 在参照模型上单线程消费一条事件。
// duplicateOf 非空表示这是一次「网络重试的重复投递」（同一 EventID 已投递过）；
// EventID 不同的事件一律视为独立调用，哪怕内容完全等价。
func (m *NaiveModel) Deliver(ev Event) HandleResult {
	if m.seen[ev.EventID] {
		return HandleResult{EventID: ev.EventID, Outcome: OutcomeCompleted}
	}
	m.seen[ev.EventID] = true
	if m.undone[ev.EventID] {
		return HandleResult{EventID: ev.EventID, Outcome: OutcomeSuperseded}
	}
	spec, ok := m.regs.Lookup(ev.ActionType)
	if !ok {
		return HandleResult{EventID: ev.EventID, Outcome: OutcomeCompleted}
	}
	// 朴素模型：一次性顺序施加全部副作用（幂等实现保证与真实系统逐项提交同结果）。
	txn := &naiveTxn{kvs: m.kvs}
	for i, fx := range spec.Effects {
		if err := fx.Apply(nil, txn, ev, i); err != nil {
			panic("naive model effect failure: " + err.Error())
		}
	}
	return HandleResult{EventID: ev.EventID, Outcome: OutcomeCompleted}
}

// Get 读取参照模型的最终键值。
func (m *NaiveModel) Get(key string) ([]byte, bool) {
	v, ok := m.kvs[key]
	return v, ok
}

// Keys 返回排序后的全部键。
func (m *NaiveModel) Keys() []string {
	out := make([]string, 0, len(m.kvs))
	for k := range m.kvs {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// naiveTxn 是参照模型使用的内存事务（单线程，无需并发控制）。
type naiveTxn struct {
	kvs map[string][]byte
}

func (t *naiveTxn) Get(key string) ([]byte, error) {
	v, ok := t.kvs[key]
	if !ok {
		return nil, ErrNotFound
	}
	return cloneBytes(v), nil
}

func (t *naiveTxn) Put(key string, value []byte) {
	t.kvs[key] = cloneBytes(value)
}

func (t *naiveTxn) Delete(key string) { delete(t.kvs, key) }
