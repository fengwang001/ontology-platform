package lifecycle

// Quota 是免费检索额度状态的只读快照。
type Quota struct {
	Period int64 // 当前周期号 floor(maxNow/30)（实际最后一次访问所在周期）
	Used   int64 // 当前周期已用免费检索量
}

// QuotaState 返回当前周期号与该周期已用免费检索量。
func (b *Billing) QuotaState() Quota {
	b.mu.Lock()
	defer b.mu.Unlock()
	return Quota{Period: b.period, Used: b.used}
}

// MaxNow 返回已接受的最大 now。
func (b *Billing) MaxNow() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.maxNow
}

// Lookup 返回对象副本及其是否存在。
func (b *Billing) Lookup(key string) (Object, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	obj, ok := b.objs[key]
	return obj, ok
}

// Keys 返回当前全部对象键（无序）。
func (b *Billing) Keys() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	keys := make([]string, 0, len(b.objs))
	for k := range b.objs {
		keys = append(keys, k)
	}
	return keys
}
