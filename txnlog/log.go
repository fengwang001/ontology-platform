// Package txnlog 实现事务消息分区日志：数据记录与提交/中止标记交错追加，
// 通过高水位与稳定位点（LSO）为已提交读（read-committed）提供可复现的可见性边界。
package txnlog

import (
	"errors"
	"fmt"
	"sync"
)

// 错误类别：每一次非法输入都会整体拒绝，且互不相同的错误值可用于区分原因。
var (
	// ErrInvalidProducer 生产者标识非法（空串）。
	ErrInvalidProducer = errors.New("txnlog: invalid producer id")
	// ErrNoOngoingTransaction 该生产者没有进行中的事务却写入提交/中止标记。
	ErrNoOngoingTransaction = errors.New("txnlog: no ongoing transaction for producer")
	// ErrInvalidHighWatermark 高水位非法（回退或超过日志末端）。
	ErrInvalidHighWatermark = errors.New("txnlog: invalid high watermark")
	// ErrInvalidReadStart 读取起点非法（负值或超过稳定位点）。
	ErrInvalidReadStart = errors.New("txnlog: invalid read start offset")
	// ErrInvalidReadLimit 读取条数上限非法（非正数）。
	ErrInvalidReadLimit = errors.New("txnlog: invalid read limit")
)

// RecordKind 记录类型。
type RecordKind int

const (
	// KindData 数据记录，属于某个事务。
	KindData RecordKind = iota
	// KindCommit 提交标记，结束所属生产者当前事务且判定为已提交。
	KindCommit
	// KindAbort 中止标记，结束所属生产者当前事务且判定为已中止。
	KindAbort
)

func (k RecordKind) String() string {
	switch k {
	case KindData:
		return "data"
	case KindCommit:
		return "commit"
	case KindAbort:
		return "abort"
	default:
		return "unknown"
	}
}

// Record 是日志中的一条记录。控制标记（提交/中止）永远不会被已提交读返回。
type Record struct {
	Offset   int64      // 追加时分配的连续位点
	Producer string     // 写入者（生产者）标识
	Kind     RecordKind // 记录类型
	Payload  []byte     // 仅数据记录有效
}

// txn 描述一个事务：某生产者自上一个标记之后写入的全部数据记录。
type txn struct {
	producer     string
	firstOffset  int64 // 事务首位点：该事务第一条数据的位点
	decided      bool  // 是否已写入结束标记
	committed    bool  // 结束标记为提交还是中止
	markerOffset int64 // 结束标记的位点（decided 时有效）
}

// entry 是日志内部存储单元，把记录与其所属事务关联起来。
type entry struct {
	rec Record
	txn *txn // 仅数据记录非空
}

// Log 是单分区事务日志。所有导出方法都可被多个执行体并发调用。
type Log struct {
	mu      sync.RWMutex
	entries []entry         // 追加即分配连续位点，位点即下标
	hw      int64           // 高水位：位点 < hw 的记录对消费者可见，只进不退
	ongoing map[string]*txn // 生产者 -> 进行中事务（同一生产者至多一个）
	txns    []*txn          // 全部事务，按首位点递增排列
}

// New 创建一个空日志，高水位为 0。
func New() *Log {
	return &Log{ongoing: make(map[string]*txn)}
}

// AppendData 为 producer 追加一条数据记录，返回分配的连续位点。
// 若该生产者当前没有进行中的事务，则以本记录位点作为新事务的首位点隐式开启一个事务。
func (l *Log) AppendData(producer string, payload []byte) (int64, error) {
	if producer == "" {
		return 0, ErrInvalidProducer
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	t, ok := l.ongoing[producer]
	if !ok {
		t = &txn{producer: producer, firstOffset: int64(len(l.entries))}
		l.ongoing[producer] = t
		l.txns = append(l.txns, t)
	}
	offset := int64(len(l.entries))
	cp := make([]byte, len(payload))
	copy(cp, payload)
	l.entries = append(l.entries, entry{
		rec: Record{Offset: offset, Producer: producer, Kind: KindData, Payload: cp},
		txn: t,
	})
	return offset, nil
}

// AppendMarker 为 producer 追加一条结束标记（commit=true 提交，false 中止），
// 结束其当前事务。若该生产者没有进行中的事务则整体拒绝。
func (l *Log) AppendMarker(producer string, commit bool) (int64, error) {
	if producer == "" {
		return 0, ErrInvalidProducer
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	t, ok := l.ongoing[producer]
	if !ok {
		return 0, fmt.Errorf("%w: %q", ErrNoOngoingTransaction, producer)
	}
	kind := KindAbort
	if commit {
		kind = KindCommit
	}
	offset := int64(len(l.entries))
	l.entries = append(l.entries, entry{
		rec: Record{Offset: offset, Producer: producer, Kind: kind},
	})
	t.decided = true
	t.committed = commit
	t.markerOffset = offset
	delete(l.ongoing, producer)
	return offset, nil
}

// AdvanceHighWatermark 把高水位推进到 newHW。newHW 必须不小于当前高水位
// 且不超过日志末端位点，否则整体拒绝且状态不变。
func (l *Log) AdvanceHighWatermark(newHW int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if newHW < l.hw || newHW > int64(len(l.entries)) {
		return fmt.Errorf("%w: %d not in [%d, %d]", ErrInvalidHighWatermark, newHW, l.hw, len(l.entries))
	}
	l.hw = newHW
	return nil
}

// HighWatermark 返回当前高水位。
func (l *Log) HighWatermark() int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.hw
}

// LogEndOffset 返回日志末端位点（下一条记录的位点）。
func (l *Log) LogEndOffset() int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return int64(len(l.entries))
}

// StableOffset 返回稳定位点：高水位与所有“首位点低于高水位且尚无可见结论”的
// 事务首位点中的较小者。结论可见指结束标记已写入且标记位点低于高水位。
// 稳定位点只进不退。
func (l *Log) StableOffset() int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.stableOffsetLocked()
}

func (l *Log) stableOffsetLocked() int64 {
	stable := l.hw
	for _, t := range l.txns {
		if t.firstOffset >= stable {
			break // 事务按首位点递增，之后的都不可能再拉低稳定位点
		}
		if !t.decided || t.markerOffset >= l.hw {
			stable = t.firstOffset
		}
	}
	return stable
}

// ReadCommitted 已提交读：返回位点位于 [from, 稳定位点) 内、所属事务以提交结束的
// 数据记录，至多 limit 条；绝不返回控制标记，也绝不暴露未决或已中止事务的数据。
// from 必须位于 [0, 稳定位点]，limit 必须为正，否则整体拒绝。
func (l *Log) ReadCommitted(from int64, limit int) ([]Record, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	stable := l.stableOffsetLocked()
	if from < 0 || from > stable {
		return nil, fmt.Errorf("%w: %d not in [0, %d]", ErrInvalidReadStart, from, stable)
	}
	if limit <= 0 {
		return nil, fmt.Errorf("%w: %d", ErrInvalidReadLimit, limit)
	}
	out := make([]Record, 0, min(limit, int(stable-from)))
	for off := from; off < stable && len(out) < limit; off++ {
		e := l.entries[off]
		if e.rec.Kind != KindData {
			continue // 控制标记绝不返回
		}
		// off < stable 蕴含所属事务已有可见结论，此处仍显式校验提交判定。
		if e.txn != nil && e.txn.decided && e.txn.committed {
			out = append(out, e.rec)
		}
	}
	return out, nil
}
