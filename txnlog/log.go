// Package txnlog 实现事务消息分区日志：数据记录与提交/中止标记交错追加，
// 维护高水位与稳定位点，并提供只返回已提交数据的已提交读。
package txnlog

import (
	"errors"
	"sort"
	"sync"
)

// 各类非法输入对应的可区分错误，均可用 errors.Is 判定。
var (
	// ErrUnknownProducer 生产者未注册或标识为空。
	ErrUnknownProducer = errors.New("txnlog: unknown producer")
	// ErrNoOngoingTransaction 生产者当前没有进行中的事务却写入标记。
	ErrNoOngoingTransaction = errors.New("txnlog: no ongoing transaction")
	// ErrInvalidHighWatermark 高水位倒退或越过日志末尾。
	ErrInvalidHighWatermark = errors.New("txnlog: invalid high watermark")
	// ErrInvalidReadStart 读取起点为负或越过稳定位点。
	ErrInvalidReadStart = errors.New("txnlog: invalid read start")
)

// RecordKind 记录类别：数据记录或控制标记。
type RecordKind int

const (
	// KindData 数据记录。
	KindData RecordKind = iota
	// KindCommit 提交标记。
	KindCommit
	// KindAbort 中止标记。
	KindAbort
)

// Record 日志中的一条记录。
type Record struct {
	Offset     int64
	ProducerID string
	Kind       RecordKind
	Payload    []byte
}

// Log 事务分区日志，可被多个执行体并发调用。
type Log struct {
	mu        sync.Mutex
	records   []Record
	hw        int64
	stable    int64
	producers map[string]*producerState
}

// decision 一个已有结论的事务：覆盖位点区间 [firstOffset, endOffset)。
type decision struct {
	firstOffset int64
	endOffset   int64 // 结束该事务的标记位点
	committed   bool
}

// producerState 单个生产者的事务状态。
type producerState struct {
	openFirst int64      // 进行中事务的首位点，-1 表示无进行中事务
	decisions []decision // 已结束事务，按 firstOffset 升序
}

// New 创建空日志。
func New() *Log {
	return &Log{
		producers: make(map[string]*producerState),
	}
}

// RegisterProducer 注册生产者，之后才能以其身份追加记录。
// 空标识为非法生产者；重复注册为幂等成功。
func (l *Log) RegisterProducer(id string) error {
	if id == "" {
		return ErrUnknownProducer
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.producers[id]; !ok {
		l.producers[id] = &producerState{openFirst: -1}
	}
	return nil
}

// AppendData 为生产者追加一条数据记录，返回其位点。
// 若该生产者无进行中事务，则以本条记录位点开启新事务。
func (l *Log) AppendData(producerID string, payload []byte) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	p, ok := l.producers[producerID]
	if !ok {
		return 0, ErrUnknownProducer
	}
	offset := int64(len(l.records))
	if p.openFirst < 0 {
		p.openFirst = offset
	}
	l.records = append(l.records, Record{
		Offset:     offset,
		ProducerID: producerID,
		Kind:       KindData,
		Payload:    append([]byte(nil), payload...),
	})
	return offset, nil
}

// AppendMarker 为生产者追加提交/中止标记，结束其进行中的事务。
// 控制标记对任何读取都不可见。
func (l *Log) AppendMarker(producerID string, commit bool) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	p, ok := l.producers[producerID]
	if !ok {
		return 0, ErrUnknownProducer
	}
	if p.openFirst < 0 {
		return 0, ErrNoOngoingTransaction
	}
	offset := int64(len(l.records))
	kind := KindAbort
	if commit {
		kind = KindCommit
	}
	l.records = append(l.records, Record{
		Offset:     offset,
		ProducerID: producerID,
		Kind:       kind,
	})
	p.decisions = append(p.decisions, decision{
		firstOffset: p.openFirst,
		endOffset:   offset,
		committed:   commit,
	})
	p.openFirst = -1
	l.refreshStableLocked()
	return offset, nil
}

// AdvanceHighWatermark 推进高水位，只进不退且不超过日志末尾。
func (l *Log) AdvanceHighWatermark(hw int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if hw < l.hw || hw > int64(len(l.records)) {
		return ErrInvalidHighWatermark
	}
	if hw == l.hw {
		return nil
	}
	l.hw = hw
	l.refreshStableLocked()
	return nil
}

// HighWatermark 返回当前高水位。
func (l *Log) HighWatermark() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.hw
}

// StableOffset 返回当前稳定位点：高水位与所有首位点低于高水位且
// 尚无可见结论的事务首位点中的较小者，只进不退。
func (l *Log) StableOffset() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.stable
}

// refreshStableLocked 重算稳定位点，调用方须持有锁。
// 新事务首位点即日志末尾、不小于高水位，且高水位只进不退，
// 因此计算结果天然单调，仍以 max 兜底保证只进不退。
func (l *Log) refreshStableLocked() {
	stable := l.hw
	for _, p := range l.producers {
		if p.openFirst >= 0 && p.openFirst < stable {
			stable = p.openFirst
		}
		if n := len(p.decisions); n > 0 {
			last := p.decisions[n-1]
			if last.endOffset >= l.hw && last.firstOffset < stable {
				stable = last.firstOffset
			}
		}
	}
	if stable > l.stable {
		l.stable = stable
	}
}

// ReadCommitted 从 from 起返回稳定位点之前、所属事务已提交的数据记录。
// 结果绝不包含控制标记，也不包含未决或已中止事务的数据，可复现。
func (l *Log) ReadCommitted(from int64, maxCount int) ([]Record, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if from < 0 || from > l.stable {
		return nil, ErrInvalidReadStart
	}
	if maxCount <= 0 {
		return nil, nil
	}
	var out []Record
	for i := from; i < l.stable && len(out) < maxCount; i++ {
		rec := l.records[i]
		if rec.Kind != KindData {
			continue
		}
		if l.committedLocked(rec.ProducerID, rec.Offset) {
			out = append(out, rec)
		}
	}
	return out, nil
}

// committedLocked 判定位点 offset 的数据记录所属事务是否已提交。
// 调用方须持有锁，且 offset 须低于稳定位点（结论必然已存在）。
func (l *Log) committedLocked(producerID string, offset int64) bool {
	p := l.producers[producerID]
	idx := sort.Search(len(p.decisions), func(i int) bool {
		return p.decisions[i].firstOffset > offset
	})
	if idx == 0 {
		return false
	}
	d := p.decisions[idx-1]
	return offset < d.endOffset && d.committed
}
