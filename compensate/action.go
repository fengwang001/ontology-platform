package compensate

import "context"

// Effect 是补偿动作中的一项副作用。每一项必须幂等：
// 同一 (EventID, EffectIndex) 下重复调用，可观察结果与调用一次完全一致。
// 实现方式：每项副作用只写自己拥有的、以 EventID 命名的键
// （例如补偿标记 / 反向记账记录），Apply 前先读取该键，已存在则直接返回。
type Effect interface {
	// OwnedKey 返回该项副作用独占拥有的键。处理器据此：
	// 1) 在崩溃续作时判断该项是否已经生效（无需扫描全历史）；
	// 2) 界定无关并发修改——读取集合只包含这些独占键与补偿记录，
	//    不包含被补偿对象的「其他」字段，因此无关事件修改对象其他字段不会导致续作失败。
	OwnedKey(ev Event, index int) string
	Apply(ctx context.Context, txn Txn, ev Event, index int) error
}

// CompensationSpec 是某类原始动作关联的补偿动作规格：
// 一个有序的、原子性语义下的副作用集合（全部生效或保持已确定边界）。
type CompensationSpec struct {
	ActionType string
	Effects    []Effect
	// TargetExists 在消费/续作时检查补偿目标对象是否仍然存在且未被撤销。
	TargetExists func(txn Txn, ev Event) bool
}

// Registry 按原始动作类型查找补偿规格。
type Registry struct {
	specs map[string]CompensationSpec
}

// NewRegistry 创建空注册表。
func NewRegistry() *Registry {
	return &Registry{specs: map[string]CompensationSpec{}}
}

// Register 注册一个补偿规格。
func (r *Registry) Register(spec CompensationSpec) {
	r.specs[spec.ActionType] = spec
}

// Lookup 返回某类动作的补偿规格。
func (r *Registry) Lookup(actionType string) (CompensationSpec, bool) {
	spec, ok := r.specs[actionType]
	return spec, ok
}

// DefaultHistory 是默认的历史探测器：直接探测 Effect.OwnedKey 是否已有值。
// 这是 O(1) 的定点探测，不扫描任何历史日志，开销与系统累计处理的事件总量无关。
//
// 注入故障用：测试可用 FaultyHistory 包装它，让「记录丢失/不可读」表现为
// ErrHistoryUnavailable，由处理器归类为 E2_HISTORY_MISSING。
var DefaultHistory HistoryInspector = defaultHistory{}

// ErrHistoryUnavailable 表示续作所需历史记录缺失或不可读。
type historyUnavailableError struct{ msg string }

func (e *historyUnavailableError) Error() string { return "history unavailable: " + e.msg }

type defaultHistory struct{}

func (defaultHistory) EffectApplied(txn Txn, ev Event, fx Effect, index int) (bool, error) {
	_, err := txn.Get(fx.OwnedKey(ev, index))
	if err == nil {
		return true, nil
	}
	if err == ErrNotFound {
		return false, nil
	}
	return false, err
}

// DefaultEffectKey 是副作用独占键的统一命名规则。
func DefaultEffectKey(ev Event, index int) string {
	return "comp/fx/" + ev.EventID + "/" + itoa(index)
}

// MarkerEffect 是最常用的补偿副作用实现：在自己独占的键上写入幂等标记，
// 并可顺带修改一个业务对象键（模拟「对某对象的补偿性副作用」）。
// 业务键的修改也以独占标记的存在为准：已存在标记 ⇒ 本项整体视为已生效，
// 绝不对业务键重复施加。
type MarkerEffect struct {
	// BusinessKey 返回本项副作用要修改的业务键；可为 nil 表示只写标记。
	BusinessKey func(ev Event) string
	// ApplyBusiness 在业务键当前值上施加一次补偿性变更（调用者保证此前未施加过）。
	ApplyBusiness func(txn Txn, key string, ev Event)
}

func (m MarkerEffect) OwnedKey(ev Event, index int) string { return DefaultEffectKey(ev, index) }

func (m MarkerEffect) Apply(_ context.Context, txn Txn, ev Event, index int) error {
	marker := m.OwnedKey(ev, index)
	if _, err := txn.Get(marker); err == nil {
		return nil // 已生效：幂等返回，不重复施加业务变更
	}
	if m.BusinessKey != nil && m.ApplyBusiness != nil {
		m.ApplyBusiness(txn, m.BusinessKey(ev), ev)
	}
	txn.Put(marker, []byte("1"))
	return nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
