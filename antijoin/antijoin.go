package antijoin

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Side 表示行所在的一侧。
type Side uint8

const (
	// Left 为反连接的左侧（被选择的一侧）。
	Left Side = iota
	// Right 为反连接的右侧（用于探测匹配的一侧）。
	Right
)

func (s Side) String() string {
	switch s {
	case Left:
		return "left"
	case Right:
		return "right"
	default:
		return fmt.Sprintf("side(%d)", int(s))
	}
}

// Op 表示变更操作；在结果变更日志中表示成员的进入/离开方向。
type Op uint8

const (
	// Insert 表示插入一行；在变更日志中表示“进入”结果。
	Insert Op = iota
	// Delete 表示删除一行；在变更日志中表示“离开”结果。
	Delete
)

func (o Op) String() string {
	switch o {
	case Insert:
		return "insert"
	case Delete:
		return "delete"
	default:
		return fmt.Sprintf("op(%d)", int(o))
	}
}

// Row 是一个由可空键组成的行（标识在映射的键中）。
type Row struct {
	Key any
}

// Change 是左右两侧行变更流中的一条事件。
// Key 为 nil 表示 SQL 语义下的 NULL：NULL 与任何值（含另一个 NULL）都不相等。
// 非空键必须是 Go 可比较类型（即可作为 map 键）。
type Change struct {
	Side Side
	Op   Op
	ID   string
	Key  any
}

func (c Change) String() string {
	return fmt.Sprintf("%s %s id=%q key=%s", c.Side, c.Op, c.ID, formatKey(c.Key))
}

// Output 是反连接结果集的一条变更日志。
type Output struct {
	Op     Op
	ID     string
	Key    any
	Reason string
}

func (o Output) String() string {
	return fmt.Sprintf("%s id=%q key=%s", o.Op, o.ID, formatKey(o.Key))
}

// 互不可混淆的拒绝原因，调用方可用 errors.Is 判别错误类别。
var (
	ErrEmptyID         = errors.New("antijoin: empty identifier")
	ErrDuplicateInsert = errors.New("antijoin: duplicate insert, identifier already exists on the same side")
	ErrDeleteNotExist  = errors.New("antijoin: delete non-existent identifier")
	ErrRowLimit        = errors.New("antijoin: row count limit exceeded")
	ErrInvalidInput    = errors.New("antijoin: invalid input")
)

// DefaultMaxRows 是默认的两侧存活行数总和上限。
const DefaultMaxRows = 1 << 20

// View 是“左行在结果中当且仅当不存在键相等的右行”的增量物化视图。
// 所有方法均支持多执行体并发：读路径共享 RLock，提交路径独占 Lock。
type View struct {
	mu sync.RWMutex

	left       map[string]Row
	right      map[string]Row
	rightCount map[any]int
	members    map[string]struct{}

	maxRows int
	seq     int
	steps   []Step
	logger  Logger
}

// Option 配置 View。
type Option func(*View)

// WithMaxRows 设置两侧存活行数总和的上限。
func WithMaxRows(n int) Option {
	return func(v *View) {
		if n >= 0 {
			v.maxRows = n
		}
	}
}

// WithLogger 注入步骤日志记录器。
func WithLogger(l Logger) Option {
	return func(v *View) {
		if l != nil {
			v.logger = l
		}
	}
}

// New 创建一个空视图。
func New(opts ...Option) *View {
	v := &View{
		left:       map[string]Row{},
		right:      map[string]Row{},
		rightCount: map[any]int{},
		members:    map[string]struct{}{},
		maxRows:    DefaultMaxRows,
		logger:     NopLogger{},
	}
	for _, opt := range opts {
		opt(v)
	}
	return v
}

// Apply 原子地提交一条行变更，返回本次输入导致的结果变更日志（同一输入多条输出按标识排序）。
// 若输入被拒绝，返回互可区分的哨兵错误，且两侧行、右侧计数、视图成员与已提交日志均不发生任何变化。
func (v *View) Apply(c Change) ([]Output, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if err := validate(c); err != nil {
		v.record(Step{Input: c, Accepted: false, Basis: err.Error()})
		return nil, err
	}

	var side map[string]Row
	switch c.Side {
	case Left:
		side = v.left
	case Right:
		side = v.right
	default:
		err := fmt.Errorf("%w: unknown side %d", ErrInvalidInput, int(c.Side))
		v.record(Step{Input: c, Accepted: false, Basis: err.Error()})
		return nil, err
	}

	if c.Op == Insert {
		if _, exists := side[c.ID]; exists {
			v.record(Step{Input: c, Accepted: false, Basis: ErrDuplicateInsert.Error()})
			return nil, ErrDuplicateInsert
		}
		if len(v.left)+len(v.right)+1 > v.maxRows {
			v.record(Step{Input: c, Accepted: false, Basis: ErrRowLimit.Error()})
			return nil, ErrRowLimit
		}
	} else if _, exists := side[c.ID]; !exists {
		v.record(Step{Input: c, Accepted: false, Basis: ErrDeleteNotExist.Error()})
		return nil, ErrDeleteNotExist
	}

	var outs []Output
	var basis string
	switch c.Side {
	case Left:
		outs, basis = v.applyLeft(c)
	case Right:
		outs, basis = v.applyRight(c)
	}

	v.seq++
	v.record(Step{Seq: v.seq, Input: c, Accepted: true, Basis: basis, Outputs: outs})
	return outs, nil
}

func validate(c Change) error {
	if c.ID == "" {
		return ErrEmptyID
	}
	switch c.Op {
	case Insert, Delete:
	default:
		return fmt.Errorf("%w: unknown op %d", ErrInvalidInput, int(c.Op))
	}
	return nil
}

// record 记录步骤：拒绝步骤只写外部日志器，不进入已提交变更日志（失败不留痕）。
func (v *View) record(s Step) {
	v.logger.Log(s)
	if s.Accepted {
		v.steps = append(v.steps, s)
	}
}

func (v *View) applyLeft(c Change) ([]Output, string) {
	if c.Op == Insert {
		v.left[c.ID] = Row{Key: c.Key}
		if c.Key == nil {
			v.members[c.ID] = struct{}{}
			return []Output{{
				Op:     Insert,
				ID:     c.ID,
				Key:    c.Key,
				Reason: "left insert with NULL key: NULL never equals anything, no right row can match -> enter",
			}}, "accepted: NULL key is always unmatched, left row enters result"
		}
		if v.rightCount[c.Key] == 0 {
			v.members[c.ID] = struct{}{}
			return []Output{{
				Op:     Insert,
				ID:     c.ID,
				Key:    c.Key,
				Reason: "left insert with non-null key and right count 0: no matching right row exists -> enter",
			}}, "accepted: non-null key with right count 0, left row enters result"
		}
		return nil, "accepted: non-null key with right count > 0, left row is blocked, no output"
	}

	stored := v.left[c.ID]
	delete(v.left, c.ID)
	if stored.Key == nil || v.rightCount[stored.Key] == 0 {
		delete(v.members, c.ID)
		return []Output{{
			Op:     Delete,
			ID:     c.ID,
			Key:    stored.Key,
			Reason: "left delete of a result member: membership removed -> leave",
		}}, "accepted: deleted left row was in result, it leaves"
	}
	return nil, "accepted: deleted left row was not in result, no output"
}

func (v *View) applyRight(c Change) ([]Output, string) {
	if c.Op == Insert {
		v.right[c.ID] = Row{Key: c.Key}
		if c.Key == nil {
			return nil, "accepted: NULL right row never matches any left row, counts untouched, no output"
		}
		old := v.rightCount[c.Key]
		v.rightCount[c.Key] = old + 1
		if old == 0 {
			return v.emitForKey(c.Key, Delete),
				"accepted: right count 0->1 for non-null key, matching left rows leave"
		}
		return nil, "accepted: right count stays positive (1->2 style), no zero crossing, no output"
	}

	stored := v.right[c.ID]
	delete(v.right, c.ID)
	if stored.Key == nil {
		return nil, "accepted: NULL right row never matched, no count or membership change"
	}
	old := v.rightCount[stored.Key]
	if old <= 1 {
		delete(v.rightCount, stored.Key)
	} else {
		v.rightCount[stored.Key] = old - 1
	}
	if old == 1 {
		return v.emitForKey(stored.Key, Insert),
			"accepted: right count 1->0 for non-null key, matching left rows enter"
	}
	return nil, "accepted: right count stays positive (2->1 style), no zero crossing, no output"
}

// emitForKey 收集所有键与 key 相等的非空键左行，按标识排序后输出进入/离开并同步成员集合。
func (v *View) emitForKey(key any, op Op) []Output {
	ids := make([]string, 0)
	for id, row := range v.left {
		if row.Key != nil && row.Key == key {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)

	reason := "right count 1->0: no right row with the same non-null key remains -> enter"
	if op == Delete {
		reason = "right count 0->1: a right row with the same non-null key now exists -> leave"
	}
	outs := make([]Output, 0, len(ids))
	for _, id := range ids {
		if op == Insert {
			v.members[id] = struct{}{}
		} else {
			delete(v.members, id)
		}
		outs = append(outs, Output{Op: op, ID: id, Key: key, Reason: reason})
	}
	return outs
}

func formatKey(key any) string {
	if key == nil {
		return "NULL"
	}
	return fmt.Sprintf("%v", key)
}

func cloneRows(src map[string]Row) map[string]Row {
	dst := make(map[string]Row, len(src))
	for id, row := range src {
		dst[id] = row
	}
	return dst
}

// Check 为自检：重新扫描两侧行，重算右侧计数与成员集合，与增量维护的状态逐项比对。
// 可被多个执行体并发调用，且可与 Apply 并发。
func (v *View) Check() error {
	v.mu.RLock()
	defer v.mu.RUnlock()

	counts := map[any]int{}
	for _, row := range v.right {
		if row.Key != nil {
			counts[row.Key]++
		}
	}
	for key, n := range counts {
		if got := v.rightCount[key]; got != n {
			return fmt.Errorf("antijoin: self-check failed, right count for %s = %d, want %d", formatKey(key), got, n)
		}
	}
	for key, got := range v.rightCount {
		if want := counts[key]; got != want {
			return fmt.Errorf("antijoin: self-check failed, stale right count for %s = %d, want %d", formatKey(key), got, want)
		}
	}

	expected := Recompute(v.left, v.right)
	if len(expected) != len(v.members) {
		return fmt.Errorf("antijoin: self-check failed, member set size = %d, want %d", len(v.members), len(expected))
	}
	for id := range expected {
		if _, ok := v.members[id]; !ok {
			return fmt.Errorf("antijoin: self-check failed, left id=%q should be a member but is not", id)
		}
	}
	for id := range v.members {
		if _, ok := expected[id]; !ok {
			return fmt.Errorf("antijoin: self-check failed, left id=%q is a member but should not be", id)
		}
	}
	return nil
}

// Recompute 按反连接语义批量重算：键为 NULL 或不存在相等键右行的左行属于结果。
func Recompute(left, right map[string]Row) map[string]struct{} {
	counts := map[any]int{}
	for _, row := range right {
		if row.Key != nil {
			counts[row.Key]++
		}
	}
	out := make(map[string]struct{}, len(left))
	for id, row := range left {
		if row.Key == nil || counts[row.Key] == 0 {
			out[id] = struct{}{}
		}
	}
	return out
}

// Result 返回当前结果成员标识集合的快照。
func (v *View) Result() map[string]struct{} {
	v.mu.RLock()
	defer v.mu.RUnlock()
	out := make(map[string]struct{}, len(v.members))
	for id := range v.members {
		out[id] = struct{}{}
	}
	return out
}

// LeftRows 返回左侧存活行的快照。
func (v *View) LeftRows() map[string]Row {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return cloneRows(v.left)
}

// RightRows 返回右侧存活行的快照。
func (v *View) RightRows() map[string]Row {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return cloneRows(v.right)
}

// RightCount 返回某个非空键当前的右侧行数（NULL 键恒为 0）。
func (v *View) RightCount(key any) int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if key == nil {
		return 0
	}
	return v.rightCount[key]
}

// Changelog 返回已接受步骤的快照（被拒绝的尝试不入日志，失败不留痕）。
func (v *View) Changelog() []Step {
	v.mu.RLock()
	defer v.mu.RUnlock()
	out := make([]Step, len(v.steps))
	copy(out, v.steps)
	return out
}
