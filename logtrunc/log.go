// Package logtrunc 实现日志截断的位点一致维护器。
//
// 核心不变量：
//   - 条目带连续偏移序号，从起始偏移 start 开始逐一递增；
//   - 持久化位点 persisted 只进不退，表示 [0, persisted) 已全部落盘；
//   - 截断分两步：先落截断标记 marker，再物理删除前缀；
//   - 恢复时比较 marker 与实际起始 actual：
//     actual == marker 干净；actual < marker 补删收敛；actual > marker 报告损坏。
package logtrunc

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// 可区分的失败原因。所有校验失败都发生在任何状态变更之前，
// 一次失败不会改变日志内容、持久化位点与截断标记。
var (
	// ErrPersistRegression 持久化声明回退（小于当前持久化位点）。
	ErrPersistRegression = errors.New("logtrunc: persisted watermark regression")
	// ErrPersistOutOfRange 持久化声明越界（超过已追加的最大偏移）。
	ErrPersistOutOfRange = errors.New("logtrunc: persisted watermark out of range")
	// ErrTruncateBeyondPersisted 截断位点超过持久化位点（前缀未全部落盘）。
	ErrTruncateBeyondPersisted = errors.New("logtrunc: truncate point beyond persisted watermark")
	// ErrTruncateOutOfRange 截断位点越界（小于实际起始或大于下一偏移）。
	ErrTruncateOutOfRange = errors.New("logtrunc: truncate point out of range")
	// ErrOverDeletion 恢复时发现实际起始大于截断标记，发生了越删，日志损坏。
	ErrOverDeletion = errors.New("logtrunc: actual start beyond truncate marker, log corrupted")
	// ErrReadOutOfRange 读区间与当前可见区间无交集。
	ErrReadOutOfRange = errors.New("logtrunc: read range out of bounds")
)

// Entry 是一条带连续偏移序号的日志条目。
type Entry struct {
	Offset  uint64 `json:"offset"`
	Payload []byte `json:"payload"`
}

// Options 控制 Log 的可选行为。
type Options struct {
	// Logger 输出操作、持久化位点、标记、实际起始与判定依据，可为 nil。
	Logger func(format string, args ...any)
	// CrashHook 在截断第一步（落标记）之后、第二步（物理删除）之前调用，
	// 仅用于测试注入崩溃。
	CrashHook func()
}

// Log 是日志截断位点一致维护器，所有方法均可并发调用。
type Log struct {
	mu        sync.RWMutex
	dir       string
	start     uint64  // 实际起始偏移：entries 为空时也能确定
	entries   []Entry // 数据文件的内存镜像，偏移从 start 开始连续
	persisted uint64  // 持久化位点：[0, persisted) 已落盘，只进不退
	marker    uint64  // 截断标记：最近一次截断第一步落盘的目标起始偏移
	logger    func(format string, args ...any)
	crashHook func()
}

// Open 打开（必要时恢复）dir 下的一致性日志。
//
// 恢复时比较截断标记 marker 与数据文件的实际起始 actual：
//   - actual == marker：干净，直接使用；
//   - actual <  marker：上次崩溃发生在两步截断之间，补删 [actual, marker) 收敛；
//   - actual >  marker：发生了越删，日志损坏，整体拒绝打开。
func Open(dir string, opts *Options) (*Log, error) {
	if opts == nil {
		opts = &Options{}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	l := &Log{dir: dir, logger: opts.Logger, crashHook: opts.CrashHook}

	raw, err := os.ReadFile(filepath.Join(dir, dataFileName))
	switch {
	case os.IsNotExist(err):
		// 全新日志：起始 0，空数据文件 + 标记 0。
		if err := writeFileAtomic(dir, dataFileName, encodeData(0, nil)); err != nil {
			return nil, err
		}
		if err := writeFileAtomic(dir, markerFileName, []byte("0\n")); err != nil {
			return nil, err
		}
		l.start = 0
		l.marker = 0
		l.logf("open: 新建日志 dir=%s persisted=%d marker=%d actual=%d 判定=全新初始化", dir, l.persisted, l.marker, 0)
	case err != nil:
		return nil, err
	default:
		actual, entries, err := decodeData(raw)
		if err != nil {
			return nil, err
		}
		marker, err := readMarker(dir, actual)
		if err != nil {
			return nil, err
		}
		next := actual + uint64(len(entries))
		switch {
		case actual == marker:
			l.start = actual
			l.entries = entries
			l.marker = marker
			l.persisted = next // 已落盘的全部内容均可视为已持久化
			l.logf("open: 判定=干净 actual=%d marker=%d 一致，无需收敛 persisted=%d", actual, marker, l.persisted)
		case actual < marker:
			// 崩溃发生在“落标记”之后、“物理删除”之前：补删收敛。
			if marker > next {
				return nil, fmt.Errorf("%w: marker=%d 超过下一偏移 %d", ErrOverDeletion, marker, next)
			}
			kept := entries[marker-actual:]
			if err := writeFileAtomic(dir, dataFileName, encodeData(marker, kept)); err != nil {
				return nil, err
			}
			l.start = marker
			l.entries = kept
			l.marker = marker
			l.persisted = next
			l.logf("open: 判定=崩溃于两步截断之间 actual=%d < marker=%d，补删 [%d,%d) 收敛为 start=%d persisted=%d",
				actual, marker, actual, marker, marker, l.persisted)
		default: // actual > marker
			l.logf("open: 判定=损坏 actual=%d > marker=%d，发生越删，拒绝打开", actual, marker)
			return nil, fmt.Errorf("%w: actual start %d > marker %d", ErrOverDeletion, actual, marker)
		}
	}
	return l, nil
}

// Append 追加若干条目，返回分配的连续偏移。
// 追加只负责落盘条目本身，持久化位点的推进由 DeclarePersisted 显式声明。
func (l *Log) Append(payloads ...[]byte) ([]uint64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	start := l.startLocked()
	next := start + uint64(len(l.entries))
	offsets := make([]uint64, len(payloads))
	for i, p := range payloads {
		offsets[i] = next + uint64(i)
		l.entries = append(l.entries, Entry{Offset: offsets[i], Payload: p})
	}
	if err := writeFileAtomic(l.dir, dataFileName, encodeData(start, l.entries)); err != nil {
		l.entries = l.entries[:len(l.entries)-len(payloads)] // 失败整体回滚
		return nil, err
	}
	l.logf("append: n=%d offsets=[%d,%d] persisted=%d marker=%d actual=%d",
		len(payloads), next, next+uint64(len(payloads)), l.persisted, l.marker, start)
	return offsets, nil
}

// DeclarePersisted 声明持久化位点推进到 upTo（不含）。
// 位点只进不退；回退或越过已追加最大偏移都整体拒绝，且不改变任何状态。
func (l *Log) DeclarePersisted(upTo uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	start := l.startLocked()
	next := start + uint64(len(l.entries))
	switch {
	case upTo < l.persisted:
		l.logf("declare-persisted: 拒绝 upTo=%d < persisted=%d 原因=回退", upTo, l.persisted)
		return fmt.Errorf("%w: %d < current %d", ErrPersistRegression, upTo, l.persisted)
	case upTo > next:
		l.logf("declare-persisted: 拒绝 upTo=%d > next=%d 原因=越界", upTo, next)
		return fmt.Errorf("%w: %d > next offset %d", ErrPersistOutOfRange, upTo, next)
	}
	l.persisted = upTo
	l.logf("declare-persisted: persisted=%d marker=%d actual=%d next=%d", l.persisted, l.marker, start, next)
	return nil
}

// Truncate 两步截断：先落截断标记 to，再物理删除 [start, to) 前缀。
// 截断位点不得超过持久化位点；越界请求整体拒绝，且不改变任何状态。
// 若在两步之间崩溃，恢复时由 Open 依据标记与实际起始补删收敛。
func (l *Log) Truncate(to uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	start := l.startLocked()
	next := start + uint64(len(l.entries))
	// 校验全部先于任何状态变更，失败即整体拒绝。
	switch {
	case to < start || to > next:
		l.logf("truncate: 拒绝 to=%d 不在 [%d,%d] 原因=截断位点越界", to, start, next)
		return fmt.Errorf("%w: %d not in [%d,%d]", ErrTruncateOutOfRange, to, start, next)
	case to > l.persisted:
		l.logf("truncate: 拒绝 to=%d > persisted=%d 原因=截断位点超过持久化位点", to, l.persisted)
		return fmt.Errorf("%w: %d > persisted %d", ErrTruncateBeyondPersisted, to, l.persisted)
	case to == start:
		l.logf("truncate: to=%d 等于 actual=%d，无需截断 persisted=%d marker=%d", to, start, l.persisted, l.marker)
		return nil
	}

	// 第一步：落截断标记（先于任何物理删除，崩溃可据此恢复）。
	if err := writeFileAtomic(l.dir, markerFileName, []byte(strconv.FormatUint(to, 10)+"\n")); err != nil {
		return err
	}
	l.marker = to
	l.logf("truncate: 第一步落标记 marker=%d persisted=%d actual=%d", to, l.persisted, start)
	if l.crashHook != nil {
		l.crashHook() // 测试注入：模拟两步之间的崩溃
	}

	// 第二步：物理删除前缀，原子重写数据文件。
	kept := l.entries[to-start:]
	if err := writeFileAtomic(l.dir, dataFileName, encodeData(to, kept)); err != nil {
		return err // 标记已落盘，恢复时会补删收敛
	}
	l.start = to
	l.entries = kept
	l.logf("truncate: 第二步物理删除 [%d,%d) 完成 persisted=%d marker=%d actual=%d", start, to, l.persisted, l.marker, to)
	return nil
}

// Read 读取 [from, to) 与当前可见区间交集的条目快照。
// 读持有读锁，快照内的偏移必然连贯；截断的两步都在写锁内完成，
// 因此读者永远看不到“标记已推进但前缀仍部分可见”的中间态。
func (l *Log) Read(from, to uint64) ([]Entry, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	start := l.startLocked()
	next := start + uint64(len(l.entries))
	if from >= to || to <= start || from >= next {
		return nil, fmt.Errorf("%w: [%d,%d) vs visible [%d,%d)", ErrReadOutOfRange, from, to, start, next)
	}
	lo := max(from, start) - start
	hi := min(to, next) - start
	out := make([]Entry, hi-lo)
	copy(out, l.entries[lo:hi])
	return out, nil
}

// Verify 自检不变量，可与任意操作并发：
// 偏移自实际起始连续递增、持久化位点不越界、标记与实际起始一致。
func (l *Log) Verify() error {
	l.mu.RLock()
	defer l.mu.RUnlock()
	start := l.startLocked()
	for i, e := range l.entries {
		if want := start + uint64(i); e.Offset != want {
			return fmt.Errorf("logtrunc: verify: non-contiguous offset: got %d want %d", e.Offset, want)
		}
	}
	next := start + uint64(len(l.entries))
	if l.persisted < start || l.persisted > next {
		return fmt.Errorf("logtrunc: verify: persisted %d out of [%d,%d]", l.persisted, start, next)
	}
	if l.marker != start {
		return fmt.Errorf("logtrunc: verify: marker %d != actual start %d", l.marker, start)
	}
	return nil
}

// Start 返回当前实际起始偏移。
func (l *Log) Start() uint64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.startLocked()
}

// Next 返回下一个待分配的偏移。
func (l *Log) Next() uint64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.startLocked() + uint64(len(l.entries))
}

// Persisted 返回当前持久化位点。
func (l *Log) Persisted() uint64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.persisted
}

// Marker 返回当前截断标记。
func (l *Log) Marker() uint64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.marker
}

func (l *Log) startLocked() uint64 {
	return l.start
}

func (l *Log) logf(format string, args ...any) {
	if l.logger != nil {
		l.logger(format, args...)
	}
}
