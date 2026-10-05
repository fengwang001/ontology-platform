// Package usage 登记消费者对数据集的成功访问与迁移确认，
// 并按 lastAccess 维护次序以支撑有界扫描的活跃判定。
package usage

import "sort"

// Ledger 为访问登记簿，非并发安全，由上层串行化。
type Ledger struct {
	datasets map[string]*datasetLedger
	scanned  int
}

type consumerRec struct {
	lastAccess int64
	acked      bool
}

type accessEntry struct {
	lastAccess int64
	consumer   string
}

type datasetLedger struct {
	recs  map[string]*consumerRec
	order []accessEntry // 按 (lastAccess, consumer) 升序，消费者唯一
}

func New() *Ledger {
	return &Ledger{datasets: make(map[string]*datasetLedger)}
}

func (l *Ledger) forDataset(dataset string) *datasetLedger {
	dl, ok := l.datasets[dataset]
	if !ok {
		dl = &datasetLedger{recs: make(map[string]*consumerRec)}
		l.datasets[dataset] = dl
	}
	return dl
}

// locate 返回 order 中键 (lastAccess, consumer) 应处的下标。
func (dl *datasetLedger) locate(lastAccess int64, consumer string) int {
	return sort.Search(len(dl.order), func(i int) bool {
		e := dl.order[i]
		return e.lastAccess > lastAccess ||
			(e.lastAccess == lastAccess && e.consumer >= consumer)
	})
}

// RecordAccess 记录一次被放行的访问，并使此前的确认作废。
func (l *Ledger) RecordAccess(dataset, consumer string, now int64) {
	dl := l.forDataset(dataset)
	if rec, ok := dl.recs[consumer]; ok {
		old := dl.locate(rec.lastAccess, consumer)
		dl.order = append(dl.order[:old], dl.order[old+1:]...)
		rec.lastAccess = now
		rec.acked = false
	} else {
		dl.recs[consumer] = &consumerRec{lastAccess: now}
	}
	at := dl.locate(now, consumer)
	dl.order = append(dl.order, accessEntry{})
	copy(dl.order[at+1:], dl.order[at:])
	dl.order[at] = accessEntry{lastAccess: now, consumer: consumer}
}

// HasAccess 报告消费者是否对数据集有过成功访问。
func (l *Ledger) HasAccess(dataset, consumer string) bool {
	dl, ok := l.datasets[dataset]
	if !ok {
		return false
	}
	_, ok = dl.recs[consumer]
	return ok
}

// Ack 记录迁移确认（调用方保证有过成功访问）。
func (l *Ledger) Ack(dataset, consumer string) {
	if dl, ok := l.datasets[dataset]; ok {
		if rec, ok := dl.recs[consumer]; ok {
			rec.acked = true
		}
	}
}

// IsActive 报告消费者在时刻 now 是否活跃（不触碰 scanned）。
func (l *Ledger) IsActive(dataset, consumer string, now, q int64) bool {
	dl, ok := l.datasets[dataset]
	if !ok {
		return false
	}
	rec, ok := dl.recs[consumer]
	return ok && !rec.acked && rec.lastAccess > now-q
}

// ActiveConsumers 返回时刻 now 的全部活跃消费者（升序），
// 并把考察的消费者数计入 scanned。
func (l *Ledger) ActiveConsumers(dataset string, now, q int64) []string {
	dl, ok := l.datasets[dataset]
	if !ok || len(dl.order) == 0 {
		return nil
	}
	threshold := now - q
	first := sort.Search(len(dl.order), func(i int) bool {
		return dl.order[i].lastAccess > threshold
	})
	// 考察边界元素一格（确认其左侧均不活跃）加全部窗口内元素。
	l.scanned++
	l.scanned += len(dl.order) - first
	var out []string
	for _, e := range dl.order[first:] {
		if !dl.recs[e.consumer].acked {
			out = append(out, e.consumer)
		}
	}
	sort.Strings(out)
	return out
}
