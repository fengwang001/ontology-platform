package ontology

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Op 表示单个键的变更类型。
type Op int

const (
	// OpUpsert 条件写入：版本严格大于当前版本时用 Value 覆盖行。
	OpUpsert Op = iota + 1
	// OpDelete 删除：版本严格大于当前版本时移除存活行并落墓碑。
	OpDelete
)

// Event 是一个可能乱序到达的键变更事件。
type Event struct {
	Key     string
	Version int64
	Op      Op
	Value   string
}

// Row 是一条存活数据行。
type Row struct {
	Key     string
	Version int64
	Value   string
}

// BatchResult 是一批事件应用后的统计。
type BatchResult struct {
	Applied int
	Ignored int
	Purged  int
}

// Stats 是存储的自检快照。
type Stats struct {
	Rows          int
	Tombstones    int
	Watermark     int64
	IgnoredEvents int64
	Commits       int64
}

// Config 控制存储参数。
type Config struct {
	// MaxEntries 存活行与墓碑条目总数上限（<=0 表示不限制）。
	MaxEntries int
	// Retention 墓碑保留参数（版本差，必须 >0）。
	Retention int64
	// Logger 用于逐步打印输入、存活行与判定依据。
	Logger Logger
}

// Logger 是最小日志接口。
type Logger interface {
	Printf(format string, args ...any)
}

// Store 是按版本条件写入、带删除墓碑的并发安全存储。
//
// 规则：
//   - 键的当前版本取存活行版本；无存活行时取墓碑版本；都没有则为 0。
//   - 事件版本严格大于当前版本才应用，否则忽略并计入忽略数。
//   - 应用写入：更新（或新建）存活行并清除该键墓碑。
//   - 应用删除：移除存活行并设置墓碑（键原本不存在也设置）。
//   - 全局水位 = 所有已提交事件版本的最大值。
//   - 批次结束后，凡 watermark - tombstoneVersion >= Retention 的墓碑被清除，
//     清除后该键回到无状态（无行、无墓碑、当前版本 0）。
type Store struct {
	mu        sync.RWMutex
	retention int64
	maxEntries int
	logger    Logger

	rows       map[string]Row
	tombstones map[string]int64
	watermark  int64
	ignored    int64
	commits    int64
}

// New 创建空存储。retention 为墓碑保留期（版本差，必须 >0），
// maxEntries<=0 表示不限制存活行与墓碑的条目总数。
func New(retention int64, maxEntries int, logger Logger) (*Store, error) {
	if retention <= 0 {
		return nil, ErrInvalidRetention
	}
	if logger == nil {
		logger = nopLogger{}
	}
	return &Store{
		retention: retention,
		maxEntries: maxEntries,
		logger:    logger,
		rows:       make(map[string]Row),
		tombstones: make(map[string]int64),
	}, nil
}

type nopLogger struct{}

func (nopLogger) Printf(string, ...any) {}

// CurrentVersion 返回某键的当前版本（存活行 > 墓碑 > 0），不修改任何状态。
func (s *Store) CurrentVersion(key string) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.currentVersionLocked(key)
}

// Commit 校验并原子应用一批事件；任何非法输入都整批拒绝、不留痕：
// 存活行、墓碑、水位、忽略数与提交数均保持拒绝前的值。
func (s *Store) Commit(ctx context.Context, events []Event) (BatchResult, error) {
	if err := ctx.Err(); err != nil {
		return BatchResult{}, err
	}
	if events == nil {
		return BatchResult{}, &RejectError{Cause: ErrNilBatch, Index: -1}
	}
	if len(events) == 0 {
		return BatchResult{}, &RejectError{Cause: ErrBatchEmpty, Index: -1}
	}
	for i, ev := range events {
		switch {
		case ev.Key == "":
			return BatchResult{}, &RejectError{Cause: ErrEmptyKey, Index: i}
		case ev.Version <= 0:
			return BatchResult{}, &RejectError{Cause: ErrBadVersion, Index: i}
		case ev.Op != OpUpsert && ev.Op != OpDelete:
			return BatchResult{}, &RejectError{Cause: ErrUnknownOp, Index: i}
		case ev.Op == OpDelete && ev.Value != "":
			return BatchResult{}, &RejectError{Cause: ErrValueOnDelete, Index: i}
		case ev.Op == OpUpsert && ev.Value == "":
			return BatchResult{}, &RejectError{Cause: ErrMissingValue, Index: i}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// 影子副本模拟：全部判定成功后才替换真实状态，保证整批原子、失败不留痕。
	rows := make(map[string]Row, len(s.rows)+len(events))
	for k, v := range s.rows {
		rows[k] = v
	}
	tombstones := make(map[string]int64, len(s.tombstones)+len(events))
	for k, v := range s.tombstones {
		tombstones[k] = v
	}
	watermark := s.watermark
	var ignored int64

	for i, ev := range events {
		if ev.Version > watermark {
			watermark = ev.Version
		}
		current := currentVersion(rows, tombstones, ev.Key)
		switch {
		case ev.Version <= current:
			ignored++
			s.logger.Printf("input[%d] %s v=%d %s => ignored: version %d <= current %d; live=%s",
				i, ev.Key, ev.Version, opName(ev.Op), ev.Version, current, liveKeys(rows))
		case ev.Op == OpUpsert:
			_, hadTomb := tombstones[ev.Key]
			rows[ev.Key] = Row{Key: ev.Key, Version: ev.Version, Value: ev.Value}
			delete(tombstones, ev.Key)
			s.logger.Printf("input[%d] %s v=%d upsert => applied: version %d > current %d (tombstone=%v removed); live=%s",
				i, ev.Key, ev.Version, ev.Version, current, hadTomb, liveKeys(rows))
		default: // OpDelete
			delete(rows, ev.Key)
			tombstones[ev.Key] = ev.Version
			s.logger.Printf("input[%d] %s v=%d delete => applied: version %d > current %d; tombstone set; live=%s",
				i, ev.Key, ev.Version, ev.Version, current, liveKeys(rows))
		}
	}

	// 水位推进后按版本差清除到期墓碑；清除后该键回到无状态。
	purged := 0
	for k, tv := range tombstones {
		if watermark-tv >= s.retention {
			delete(tombstones, k)
			purged++
			s.logger.Printf("gc: key=%s tombstone v=%d purged (watermark %d - %d >= retention %d); live=%s",
				k, tv, watermark, tv, s.retention, liveKeys(rows))
		}
	}

	if s.maxEntries > 0 && len(rows)+len(tombstones) > s.maxEntries {
		s.logger.Printf("reject: entry count %d (rows=%d tombstones=%d) > MaxEntries %d; no state change; live=%s",
			len(rows)+len(tombstones), len(rows), len(tombstones), s.maxEntries, liveKeys(s.rows))
		return BatchResult{}, &RejectError{Cause: ErrEntryLimitExceeded, Index: -1}
	}

	s.rows = rows
	s.tombstones = tombstones
	s.watermark = watermark
	s.ignored += ignored
	s.commits++

	s.logger.Printf("committed: applied=%d ignored=%d purged=%d watermark=%d rows=%d tombstones=%d live=%s",
		len(events)-int(ignored), ignored, purged, s.watermark, len(s.rows), len(s.tombstones), liveKeys(s.rows))

	return BatchResult{Applied: len(events) - int(ignored), Ignored: int(ignored), Purged: purged}, nil
}

// Get 返回某键的存活行；不存在（含墓碑遮挡期间）时 ok=false。
func (s *Store) Get(ctx context.Context, key string) (Row, bool, error) {
	if err := ctx.Err(); err != nil {
		return Row{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.rows[key]
	return r, ok, nil
}

// List 返回全部存活行的确定性（按键升序）快照。
func (s *Store) List(ctx context.Context) ([]Row, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Row, 0, len(s.rows))
	for _, r := range s.rows {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// Watermark 返回全局水位（所有已提交事件版本的最大值），无提交时为 0。
func (s *Store) Watermark() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.watermark
}

// IgnoredCount 返回累计被忽略的事件数。
func (s *Store) IgnoredCount() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ignored
}

// Stats 返回自检快照。
func (s *Store) Stats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Stats{
		Rows:          len(s.rows),
		Tombstones:    len(s.tombstones),
		Watermark:     s.watermark,
		IgnoredEvents: s.ignored,
		Commits:       s.commits,
	}
}

func (s *Store) currentVersionLocked(key string) int64 {
	return currentVersion(s.rows, s.tombstones, key)
}

func currentVersion(rows map[string]Row, tombstones map[string]int64, key string) int64 {
	if r, ok := rows[key]; ok {
		return r.Version
	}
	if tv, ok := tombstones[key]; ok {
		return tv
	}
	return 0
}

func opName(op Op) string {
	switch op {
	case OpUpsert:
		return "upsert"
	case OpDelete:
		return "delete"
	default:
		return fmt.Sprintf("op(%d)", int(op))
	}
}

func liveKeys(rows map[string]Row) string {
	if len(rows) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(rows))
	for k, r := range rows {
		keys = append(keys, fmt.Sprintf("%s@v%d", k, r.Version))
	}
	sort.Strings(keys)
	return "{" + strings.Join(keys, ", ") + "}"
}

// AsReject 从错误中解出拒绝详情；非拒绝类错误时返回 nil。
func AsReject(err error) *RejectError {
	var re *RejectError
	if errors.As(err, &re) {
		return re
	}
	return nil
}
