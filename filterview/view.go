package filterview

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ChangeKind 是输出日志中一条净变化的种类。
type ChangeKind int

const (
	Retract ChangeKind = iota // 从视图撤回
	Add                       // 向视图写入
)

// Row 是源表中的一行：Key 为主键，Value 为过滤取值。
type Row struct {
	Key   string
	Value int64
}

// OpKind 标识一个输入条目的操作类型。
type OpKind int

const (
	Insert OpKind = iota
	Delete
	Update
)

// Op 是一个输入条目。更新操作必须同时提供 Old 与 New。
type Op struct {
	Kind OpKind
	Old  Row
	New  Row
}

// Change 是输出日志中的一条净变化。
type Change struct {
	Seq   int
	Kind  ChangeKind
	Row   Row
	Basis string
}

// JournalEntry 记录单个输入条目、其判定依据与产生的净变化。
type JournalEntry struct {
	Op       Op
	Accepted bool
	Basis    string
	Changes  []Change
}

var (
	ErrInvalidParams   = errors.New("invalid filter parameters")
	ErrEmptyKey        = errors.New("empty row key")
	ErrKeyMismatch     = errors.New("update old/new key mismatch")
	ErrKeyExists       = errors.New("primary key already exists")
	ErrKeyNotFound     = errors.New("primary key not found")
	ErrBeforeImageDiff = errors.New("before image differs from current source row")
)

// View 是维护左闭右开区间 [Low, High) 过滤视图的组件。
// 所有方法可被并发调用：Apply 串行化写入，读取方法在读写锁保护下
// 返回深拷贝，因此读侧看到的源表/视图/日志逐字段一致。
type View struct {
	mu     sync.RWMutex
	low    int64
	high   int64
	source map[string]Row
	log    []Change
	seq    int
}

// New 创建一个空视图。low >= high 视为非法参数。
func New(low, high int64) (*View, error) {
	if low >= high {
		return nil, fmt.Errorf("%w: empty or inverted interval [%d, %d)", ErrInvalidParams, low, high)
	}
	return &View{
		low:    low,
		high:   high,
		source: make(map[string]Row),
	}, nil
}

// Matches 报告给定取值是否落入左闭右开区间 [Low, High)。
// 端点规则：value == Low 满足；value == High 不满足。
func (v *View) Matches(value int64) bool {
	return v.low <= value && value < v.high
}

// Low / High 返回过滤区间端点。
func (v *View) Low() int64  { return v.low }
func (v *View) High() int64 { return v.high }

// Apply 原子地应用一批输入：先在源表的影子拷贝上逐条校验与计算，
// 任一条目被拒绝则整批放弃（源表、视图、已产生日志均不变），
// 全部通过后一次性提交。返回值按输入顺序记录每条输入的判定依据
// 与其产生的净变化，可直接用于打印处理日志。
func (v *View) Apply(ops []Op) ([]JournalEntry, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	shadow := make(map[string]Row, len(v.source))
	for k, row := range v.source {
		shadow[k] = row
	}
	nextSeq := v.seq
	journal := make([]JournalEntry, 0, len(ops))
	pending := make([]Change, 0)

	emit := func(kind ChangeKind, row Row, basis string) Change {
		nextSeq++
		return Change{Seq: nextSeq, Kind: kind, Row: row, Basis: basis}
	}

	for i, op := range ops {
		entry := JournalEntry{Op: op}
		var changes []Change

		switch op.Kind {
		case Insert:
			if op.New.Key == "" {
				return journal, fmt.Errorf("batch entry %d (insert): %w", i, ErrEmptyKey)
			}
			if _, exists := shadow[op.New.Key]; exists {
				return journal, fmt.Errorf("batch entry %d (insert key %q): %w", i, op.New.Key, ErrKeyExists)
			}
			shadow[op.New.Key] = op.New
			if v.Matches(op.New.Value) {
				entry.Basis = fmt.Sprintf("插入：值 %d 落入区间 [%d,%d)，写入视图", op.New.Value, v.low, v.high)
				changes = append(changes, emit(Add, op.New, entry.Basis))
			} else {
				entry.Basis = fmt.Sprintf("插入：值 %d 不在区间 [%d,%d) 内，视图无变化", op.New.Value, v.low, v.high)
			}

		case Delete:
			if op.Old.Key == "" {
				return journal, fmt.Errorf("batch entry %d (delete): %w", i, ErrEmptyKey)
			}
			current, ok := shadow[op.Old.Key]
			if !ok {
				return journal, fmt.Errorf("batch entry %d (delete key %q): %w", i, op.Old.Key, ErrKeyNotFound)
			}
			delete(shadow, op.Old.Key)
			if v.Matches(current.Value) {
				entry.Basis = fmt.Sprintf("删除：被删行值 %d 在区间内，从视图撤回", current.Value)
				changes = append(changes, emit(Retract, current, entry.Basis))
			} else {
				entry.Basis = fmt.Sprintf("删除：被删行值 %d 不在区间内，视图无变化", current.Value)
			}

		case Update:
			if op.Old.Key == "" || op.New.Key == "" {
				return journal, fmt.Errorf("batch entry %d (update): %w", i, ErrEmptyKey)
			}
			if op.Old.Key != op.New.Key {
				return journal, fmt.Errorf("batch entry %d (update %q -> %q): %w", i, op.Old.Key, op.New.Key, ErrKeyMismatch)
			}
			current, ok := shadow[op.Old.Key]
			if !ok {
				return journal, fmt.Errorf("batch entry %d (update key %q): %w", i, op.Old.Key, ErrKeyNotFound)
			}
			if current != op.Old {
				return journal, fmt.Errorf("batch entry %d (update key %q): %w: have %+v, claimed %+v", i, op.Old.Key, ErrBeforeImageDiff, current, op.Old)
			}
			oldIn := v.Matches(op.Old.Value)
			newIn := v.Matches(op.New.Value)
			shadow[op.New.Key] = op.New
			switch {
			case oldIn && newIn && op.Old.Value == op.New.Value:
				entry.Basis = fmt.Sprintf("更新：前后值均为 %d 且在区间内，取值不变，无输出", op.Old.Value)
			case oldIn && newIn:
				entry.Basis = fmt.Sprintf("更新：值 %d -> %d 均在区间内，先撤回旧值再写入新值", op.Old.Value, op.New.Value)
				changes = append(changes, emit(Retract, op.Old, entry.Basis+"（撤回旧值）"))
				changes = append(changes, emit(Add, op.New, entry.Basis+"（写入新值）"))
			case oldIn && !newIn:
				entry.Basis = fmt.Sprintf("更新：值 %d 在区间内、新值 %d 不在区间内，撤回旧值", op.Old.Value, op.New.Value)
				changes = append(changes, emit(Retract, op.Old, entry.Basis))
			case !oldIn && newIn:
				entry.Basis = fmt.Sprintf("更新：旧值 %d 不在区间内、新值 %d 在区间内，写入新值", op.Old.Value, op.New.Value)
				changes = append(changes, emit(Add, op.New, entry.Basis))
			default:
				entry.Basis = fmt.Sprintf("更新：旧值 %d、新值 %d 均不在区间内，视图无变化", op.Old.Value, op.New.Value)
			}

		default:
			return journal, fmt.Errorf("batch entry %d: %w: unknown op kind %d", i, ErrInvalidParams, op.Kind)
		}

		entry.Accepted = true
		entry.Changes = changes
		journal = append(journal, entry)
		pending = append(pending, changes...)
	}

	v.source = shadow
	v.log = append(v.log, pending...)
	v.seq = nextSeq
	return journal, nil
}

// Snapshot 返回视图中当前所有行的深拷贝，按 Key 排序。
func (v *View) Snapshot() []Row {
	v.mu.RLock()
	defer v.mu.RUnlock()

	out := make([]Row, 0, len(v.source))
	for _, row := range v.source {
		if v.Matches(row.Value) {
			out = append(out, row)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Log 返回已产生的全部净变化日志的拷贝（按提交序号排列）。
func (v *View) Log() []Change {
	v.mu.RLock()
	defer v.mu.RUnlock()

	out := make([]Change, len(v.log))
	copy(out, v.log)
	return out
}

// Get 读取源表当前行；第二返回值表示是否存在。
func (v *View) Get(key string) (Row, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()

	row, ok := v.source[key]
	return row, ok
}

// Contains 报告当前视图中是否存在指定主键。
func (v *View) Contains(key string) bool {
	row, ok := v.Get(key)
	return ok && v.Matches(row.Value)
}

// SourceSnapshot 返回源表全量行的深拷贝，按 Key 排序。
func (v *View) SourceSnapshot() []Row {
	v.mu.RLock()
	defer v.mu.RUnlock()

	out := make([]Row, 0, len(v.source))
	for _, row := range v.source {
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// String 返回变化种类的可读名称。
func (k ChangeKind) String() string {
	switch k {
	case Retract:
		return "RETRACT"
	case Add:
		return "ADD"
	default:
		return "UNKNOWN"
	}
}

// String 返回操作类型的可读名称。
func (k OpKind) String() string {
	switch k {
	case Insert:
		return "INSERT"
	case Delete:
		return "DELETE"
	case Update:
		return "UPDATE"
	default:
		return "UNKNOWN"
	}
}
