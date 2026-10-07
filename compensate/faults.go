package compensate

import "sync"

// CrashError 表示在副作用提交「之后」模拟的进程崩溃：副作用所在事务
// 已提交，但调用方再也收不到正常返回（模拟 kill -9）。
var CrashError = &TerminalError{Class: ErrorClass("INJECTED_CRASH"),
	msg: "injected crash after commit (simulating kill -9)"}

// CrashHook 在每次副作用事务即将正常返回前触发；返回 true 表示此刻崩溃。
// 注意钩子在 Commit 临界区之外生效：txn 的写已经原子提交，消费者随后「死亡」。
type CrashHook func(ev Event, effectIndex int) bool

// FaultyHistory 包装真实历史探测器，可按 (EventID, effectIndex) 注入
// 「历史记录缺失/不可读」故障，用于验证 E2_HISTORY_MISSING 的判定与冻结。
type FaultyHistory struct {
	Base HistoryInspector

	mu      sync.Mutex
	missing map[string]bool // key = EventID + "/" + index
}

// MakeMissing 让对指定事件指定副作用下标的历史探测返回不可用。
func (f *FaultyHistory) MakeMissing(eventID string, effectIndex int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.missing == nil {
		f.missing = map[string]bool{}
	}
	f.missing[eventID+"/"+itoa(effectIndex)] = true
}

func (f *FaultyHistory) EffectApplied(txn Txn, ev Event, fx Effect, index int) (bool, error) {
	f.mu.Lock()
	miss := f.missing[ev.EventID+"/"+itoa(index)]
	f.mu.Unlock()
	if miss {
		return false, &historyUnavailableError{msg: "injected loss for " + ev.EventID + "/" + itoa(index)}
	}
	return f.Base.EffectApplied(txn, ev, fx, index)
}

// CrashAwareProcessor 是支持崩溃注入的处理器（与 Processor 行为一致，
// 仅在每项事务提交成功后检查 CrashEffect.Crashed）。
type CrashAwareProcessor struct {
	*Processor
}

// NewCrashAwareProcessor 构造支持崩溃注入的处理器。
func NewCrashAwareProcessor(cfg Config) *CrashAwareProcessor {
	p := NewProcessor(cfg)
	return &CrashAwareProcessor{Processor: p}
}

// SetCrashHook 安装崩溃钩子：钩子在副作用事务提交成功后、推进到下一项前触发。
// 一次性使用由钩子实现自行控制（典型做法见 OneShotHook）。
func (c *CrashAwareProcessor) SetCrashHook(h CrashHook) {
	c.crashHook = h
}

// OneShotHook 返回只触发一次的崩溃钩子：仅当 (EventID,index) 命中 target 时返回 true。
func OneShotHook(targetEventID string, targetIndex int) CrashHook {
	var fired bool
	var mu sync.Mutex
	return func(ev Event, index int) bool {
		mu.Lock()
		defer mu.Unlock()
		if fired || ev.EventID != targetEventID || index != targetIndex {
			return false
		}
		fired = true
		return true
	}
}
