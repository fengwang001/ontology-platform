package ontology

import "sync"

// Log 是只增的执行记录日志。序号由引擎在持存储锁时分配，
// 自身的互斥锁保证并发查询安全。
type Log struct {
	mu      sync.Mutex
	records []*Record
}

// NewLog 创建空日志。
func NewLog() *Log {
	return &Log{}
}

// append 产出一条不可变执行记录，序号严格递增、无空洞、不重号。
func (l *Log) append(action string, params map[string]any, impacts []ImpactItem) *Record {
	l.mu.Lock()
	defer l.mu.Unlock()
	r := &Record{
		Seq:     int64(len(l.records) + 1),
		Action:  action,
		Params:  deepCopy(params).(map[string]any),
		Impacts: append([]ImpactItem(nil), impacts...),
	}
	l.records = append(l.records, r)
	return r
}

// Range 返回序号区间 [from, to] 内的记录，按序号稳定升序。
// to <= 0 表示取到最新；from 从 1 开始。
func (l *Log) Range(from, to int64) []*Record {
	l.mu.Lock()
	defer l.mu.Unlock()
	if from < 1 {
		from = 1
	}
	if to <= 0 || to > int64(len(l.records)) {
		to = int64(len(l.records))
	}
	if from > to {
		return nil
	}
	out := make([]*Record, 0, to-from+1)
	for _, r := range l.records[from-1 : to] {
		cp := *r
		cp.Params = deepCopy(r.Params).(map[string]any)
		cp.Impacts = append([]ImpactItem(nil), r.Impacts...)
		out = append(out, &cp)
	}
	return out
}

// LastSeq 返回最新一条记录的序号，无记录时为 0。
func (l *Log) LastSeq() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return int64(len(l.records))
}
