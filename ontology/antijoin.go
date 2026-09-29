// Package ontology 提供增量物化视图能力。
//
// 本包实现反连接（anti-join）增量物化视图：左侧行在结果中，
// 当且仅当不存在键与其相等的右侧行。键的相等采用 SQL 风格的
// 空值语义——空值与任何值（含另一个空值）都不相等。
package ontology

import (
	"fmt"
	"sort"
	"sync"
)

// Side 表示一次行变更所属的一侧。
type Side int

const (
	// Left 是反连接的左侧（被保留候选侧）。
	Left Side = iota
	// Right 是反连接的右侧（用于排除左侧的匹配侧）。
	Right
)

// Op 表示对一行的变更操作类型。
type Op int

const (
	// Insert 插入一行；同一侧不允许重复插入同一标识。
	Insert Op = iota
	// Delete 删除一行；删除的标识必须在该侧已存在。
	Delete
)

// Change 描述对左侧或右侧单个行的一次变更。
// Key 为 nil 表示空值键；空值键不与任何值（含另一个空值）相等。
// Delete 不检查 Key，以被删除行当前保存的键为准。
type Change struct {
	Side Side
	Op   Op
	ID   string
	Key  *string
}

// EventKind 是变更日志事件的种类。
type EventKind int

const (
	// Enter 表示一个左侧行进入物化视图结果。
	Enter EventKind = iota
	// Leave 表示一个左侧行离开物化视图结果。
	Leave
)

// Event 是物化视图输出的一条变更日志。
type Event struct {
	Kind EventKind
	ID   string
	Key  *string
}

// DefaultMaxRows 是每侧允许容纳的最大行数的默认值。
const DefaultMaxRows = 1_000_000

// RejectReason 描述一次输入被整体拒绝的可区分原因。
type RejectReason string

// 拒绝原因：每一种非法输入都有互不相同、可区分的原因。
const (
	// ReasonEmptyID：行标识为空字符串。
	ReasonEmptyID RejectReason = "empty id: row identifier must not be empty"
	// ReasonDuplicateInsert：同侧重复插入已存在的标识（含同一批内先插后插）。
	ReasonDuplicateInsert RejectReason = "duplicate insert: id already exists on the same side"
	// ReasonDeleteMissing：删除一个同侧并不存在的标识（含同批先删后删）。
	ReasonDeleteMissing RejectReason = "delete missing: id does not exist on the same side"
	// ReasonTooManyRows：提交后某侧行数会超过上限。
	ReasonTooManyRows RejectReason = "too many rows: side row count would exceed the limit"
	// ReasonInvalidSide：变更使用了未知的一侧。
	ReasonInvalidSide RejectReason = "invalid side: side must be Left or Right"
	// ReasonInvalidOp：变更使用了未知的操作类型。
	ReasonInvalidOp RejectReason = "invalid op: op must be Insert or Delete"
)

// RejectedError 表示整批输入因非法而被拒绝；拒绝不留任何痕迹。
type RejectedError struct {
	Reason RejectReason
	Index  int
	Change Change
}

func sideName(s Side) string {
	switch s {
	case Left:
		return "left"
	case Right:
		return "right"
	}
	return "unknown"
}

func opName(o Op) string {
	switch o {
	case Insert:
		return "insert"
	case Delete:
		return "delete"
	}
	return "unknown"
}

// Name 返回事件种类的可读名称（enter / leave）。
func (k EventKind) Name() string {
	switch k {
	case Enter:
		return "enter"
	case Leave:
		return "leave"
	}
	return "unknown"
}

// Name 返回一侧的可读名称（left / right）。
func (s Side) Name() string { return sideName(s) }

// Name 返回操作的可读名称（insert / delete）。
func (o Op) Name() string { return opName(o) }

func (e *RejectedError) Error() string {
	return fmt.Sprintf("rejected change #%d (%s/%s id=%q): %s",
		e.Index, sideName(e.Change.Side), opName(e.Change.Op), e.Change.ID, e.Reason)
}

// View 是反连接增量物化视图。
//
// 视图维护两侧行集合、每个非空键的右侧计数，以及结果中左侧行的
// 成员资格。同一 View 可被多个执行体并发调用，自检/快照也可与
// 提交并发执行。
type View struct {
	mu sync.RWMutex

	// maxRows 是每侧允许容纳的最大行数；超限的提交被整体拒绝。
	maxRows int

	// left / right 保存两侧行：标识 -> 键（nil 表示空值键）。
	left  map[string]*string
	right map[string]*string

	// rightCount 维护每个非空键的右侧行数；只保留计数为正的键。
	rightCount map[string]int

	// members 是当前结果中的左侧行标识集合。
	members map[string]struct{}

	// log 是已成功提交所产生的完整变更日志。
	log []Event
}

// NewView 创建一个空视图。maxRows<=0 表示使用默认上限。
func NewView(maxRows int) *View {
	if maxRows <= 0 {
		maxRows = DefaultMaxRows
	}
	return &View{
		maxRows:    maxRows,
		left:       map[string]*string{},
		right:      map[string]*string{},
		rightCount: map[string]int{},
		members:    map[string]struct{}{},
	}
}

func reject(index int, c Change, reason RejectReason) *RejectedError {
	return &RejectedError{Reason: reason, Index: index, Change: c}
}

// validate 在不触碰真实状态的前提下校验整批变更：用本批内的
// inserted/deleted 影子集合模拟逐条应用后的标识可见性，并推演
// 提交后两侧行数以检查上限。任一条非法即返回错误，真实状态不变。
func (v *View) validate(changes []Change) error {
	inserted := map[string]struct{}{}
	deleted := map[string]struct{}{}
	leftSize, rightSize := len(v.left), len(v.right)

	shadowKey := func(side Side, id string) string {
		return sideName(side) + "\x00" + id
	}
	exists := func(side Side, id string) bool {
		k := shadowKey(side, id)
		if _, ok := deleted[k]; ok {
			return false
		}
		if _, ok := inserted[k]; ok {
			return true
		}
		if side == Left {
			_, ok := v.left[id]
			return ok
		}
		_, ok := v.right[id]
		return ok
	}

	for i, c := range changes {
		switch c.Side {
		case Left, Right:
		default:
			return reject(i, c, ReasonInvalidSide)
		}
		switch c.Op {
		case Insert, Delete:
		default:
			return reject(i, c, ReasonInvalidOp)
		}
		if c.ID == "" {
			return reject(i, c, ReasonEmptyID)
		}

		k := shadowKey(c.Side, c.ID)
		switch c.Op {
		case Insert:
			if exists(c.Side, c.ID) {
				return reject(i, c, ReasonDuplicateInsert)
			}
			if c.Side == Left {
				leftSize++
				if leftSize > v.maxRows {
					return reject(i, c, ReasonTooManyRows)
				}
			} else {
				rightSize++
				if rightSize > v.maxRows {
					return reject(i, c, ReasonTooManyRows)
				}
			}
			inserted[k] = struct{}{}
		case Delete:
			if !exists(c.Side, c.ID) {
				return reject(i, c, ReasonDeleteMissing)
			}
			if _, wasInserted := inserted[k]; wasInserted {
				delete(inserted, k)
			} else {
				deleted[k] = struct{}{}
			}
			if c.Side == Left {
				leftSize--
			} else {
				rightSize--
			}
		}
	}
	return nil
}

// Commit 原子地校验并应用一批变更，返回本批产生的变更日志。
// 任一变更非法则整批拒绝：两侧行、右侧计数、成员资格与已输出
// 日志均不改变（失败不留痕）。同一批的多条输出按左侧行标识
// 升序排列；同一标识在批内互相抵消的翻转不产生日志。
func (v *View) Commit(changes []Change) ([]Event, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if err := v.validate(changes); err != nil {
		return nil, err
	}

	emitted := map[string]Event{}
	emit := func(kind EventKind, id string, key *string) {
		if prev, ok := emitted[id]; ok {
			if prev.Kind != kind {
				delete(emitted, id) // 同批内 enter+leave 净效果为零
			}
			return
		}
		emitted[id] = Event{Kind: kind, ID: id, Key: key}
	}

	for _, c := range changes {
		switch c.Side {
		case Left:
			switch c.Op {
			case Insert:
				v.left[c.ID] = c.Key
				// 空值键左行永远在结果中；非空键左行在无右侧匹配时进入。
				if c.Key == nil || v.rightCount[*c.Key] == 0 {
					v.members[c.ID] = struct{}{}
					emit(Enter, c.ID, c.Key)
				}
			case Delete:
				key := v.left[c.ID]
				delete(v.left, c.ID)
				if _, ok := v.members[c.ID]; ok {
					delete(v.members, c.ID)
					emit(Leave, c.ID, key)
				}
			}
		case Right:
			switch c.Op {
			case Insert:
				v.right[c.ID] = c.Key
				if c.Key != nil {
					old := v.rightCount[*c.Key]
					v.rightCount[*c.Key] = old + 1
					// 计数 0 -> 1：该键的所有左行离开。
					if old == 0 {
						for id, lkey := range v.left {
							if lkey != nil && *lkey == *c.Key {
								if _, ok := v.members[id]; ok {
									delete(v.members, id)
									emit(Leave, id, lkey)
								}
							}
						}
					}
				}
			case Delete:
				key := v.right[c.ID]
				delete(v.right, c.ID)
				if key != nil {
					old := v.rightCount[*key]
					if old <= 1 {
						delete(v.rightCount, *key)
					} else {
						v.rightCount[*key] = old - 1
					}
					// 计数 1 -> 0：该键的所有左行进入。
					if old == 1 {
						for id, lkey := range v.left {
							if lkey != nil && *lkey == *key {
								if _, ok := v.members[id]; !ok {
									v.members[id] = struct{}{}
									emit(Enter, id, lkey)
								}
							}
						}
					}
				}
			}
		}
	}

	out := make([]Event, 0, len(emitted))
	for _, ev := range emitted {
		out = append(out, ev)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	v.log = append(v.log, out...)

	result := make([]Event, len(out))
	copy(result, out)
	return result, nil
}

// Has 报告指定左侧行当前是否在物化视图结果中，可与提交并发。
func (v *View) Has(id string) bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	_, ok := v.members[id]
	return ok
}

// memberIDsLocked 返回当前结果中按标识排序的左侧行标识。
func (v *View) memberIDsLocked() []string {
	ids := make([]string, 0, len(v.members))
	for id := range v.members {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Snapshot 返回当前结果中左侧行标识的快照，按标识排序。
func (v *View) Snapshot() []string {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.memberIDsLocked()
}

// recomputeLocked 依据两侧行集合批量重算结果，不依赖增量状态。
func (v *View) recomputeLocked() map[string]struct{} {
	matched := map[string]struct{}{}
	for _, key := range v.right {
		if key != nil {
			matched[*key] = struct{}{}
		}
	}
	res := map[string]struct{}{}
	for id, key := range v.left {
		if key == nil {
			res[id] = struct{}{} // 空值键左行永远在结果中
			continue
		}
		if _, ok := matched[*key]; !ok {
			res[id] = struct{}{}
		}
	}
	return res
}

// Recompute 忽略增量状态，依据两侧行集合批量重算结果，
// 返回按标识排序的左侧行标识。
func (v *View) Recompute() []string {
	v.mu.RLock()
	defer v.mu.RUnlock()
	ids := make([]string, 0)
	for id := range v.recomputeLocked() {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// SelfCheck 校验增量维护的成员资格与右侧计数和批量重算一致，
// 可与提交并发调用。不一致时返回描述差异的错误。
func (v *View) SelfCheck() error {
	v.mu.RLock()
	defer v.mu.RUnlock()

	want := v.recomputeLocked()
	if len(want) != len(v.members) {
		return fmt.Errorf("self-check: member count %d != recomputed %d", len(v.members), len(want))
	}
	for id := range want {
		if _, ok := v.members[id]; !ok {
			return fmt.Errorf("self-check: incremental view missing member %q", id)
		}
	}
	for id := range v.members {
		if _, ok := want[id]; !ok {
			return fmt.Errorf("self-check: incremental view has stale member %q", id)
		}
	}

	counts := map[string]int{}
	for _, key := range v.right {
		if key != nil {
			counts[*key]++
		}
	}
	if len(counts) != len(v.rightCount) {
		return fmt.Errorf("self-check: right-count key count %d != recomputed %d", len(v.rightCount), len(counts))
	}
	for key, n := range counts {
		if v.rightCount[key] != n {
			return fmt.Errorf("self-check: right count for key %q is %d, want %d", key, v.rightCount[key], n)
		}
	}
	for key, n := range v.rightCount {
		if n <= 0 {
			return fmt.Errorf("self-check: right count for key %q is non-positive %d", key, n)
		}
		if counts[key] != n {
			return fmt.Errorf("self-check: stale right count for key %q is %d, want %d", key, n, counts[key])
		}
	}
	return nil
}

// Log 返回自创建以来已成功提交的完整变更日志副本。
func (v *View) Log() []Event {
	v.mu.RLock()
	defer v.mu.RUnlock()
	out := make([]Event, len(v.log))
	copy(out, v.log)
	return out
}

// ApplyLog 将一段变更日志应用到空的成员资格状态上，返回最终
// 结果标识集合。它用于验证“任意前缀的变更日志应用后都等于
// 批量重算结果”：对日志的每个前缀应用后，结果集合应与当时的
// 物化视图一致。事件中的键仅用于结果记录，判定以事件为准。
func ApplyLog(events []Event) map[string]struct{} {
	members := map[string]struct{}{}
	for _, ev := range events {
		switch ev.Kind {
		case Enter:
			members[ev.ID] = struct{}{}
		case Leave:
			delete(members, ev.ID)
		}
	}
	return members
}
