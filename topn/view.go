package topn

import (
	"sort"
	"sync"
)

// View 在撤回式变更流上增量维护前 N 名。
// 零值不可用，必须通过 New 构造。
//
// 所有方法均并发安全：读操作拿到的是逐字段一致的快照副本，
// 写操作以互斥锁串行化，被拒绝的变更不会留下任何痕迹。
type View struct {
	mu sync.RWMutex

	n     int
	limit int

	// live 为当前全部存活行（含榜内与榜外），键 -> 分数。
	// 榜外行必须保留，以便榜内行被撤回时按排序最靠前者补位。
	live map[string]int64
	// top 为当前前 N 名，按名次（分数降序、键升序）排序，len <= n。
	top []Row
	// log 为已产生的全部日志条目，按产生顺序排列（先离开、后进入）。
	log []Entry
}

// New 创建一个前 N 名视图。
// n 为榜单大小（必须 > 0）；maxLive 为存活行数上限（0 表示不限；若非 0 则必须 >= n）。
func New(n int, maxLive int) (*View, error) {
	if n <= 0 {
		return nil, &RejectError{
			Reason: ReasonInvalidArgument,
			Msg:    "非法参数：榜单大小 n 必须为正数",
		}
	}
	if maxLive < 0 {
		return nil, &RejectError{
			Reason: ReasonInvalidArgument,
			Msg:    "非法参数：存活行数上限 maxLive 不能为负数",
		}
	}
	if maxLive != 0 && maxLive < n {
		return nil, &RejectError{
			Reason: ReasonInvalidArgument,
			Msg:    "非法参数：存活行数上限 maxLive 不能小于榜单大小 n",
		}
	}
	return &View{
		n:     n,
		limit: maxLive,
		live:  make(map[string]int64),
	}, nil
}

// Apply 处理一条变更；被拒绝时返回 *RejectError，且不改变存活行、前 N 名与日志。
// 成功时返回本次变更产生的日志条目（离开在前、进入在后），榜单无变化时为 nil。
func (v *View) Apply(c Change) ([]Entry, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	// 1. 校验输入。校验全部发生在状态修改之前，拒绝时不产生任何副作用。
	if c.Key == "" {
		return nil, &RejectError{
			Reason: ReasonInvalidArgument,
			Msg:    "非法输入：键不能为空",
		}
	}
	switch c.Op {
	case OpAdd:
		if _, ok := v.live[c.Key]; ok {
			return nil, &RejectError{
				Reason: ReasonDuplicateKey,
				Msg:    "拒绝新增：键 " + c.Key + " 已存活",
			}
		}
		if v.limit != 0 && len(v.live) >= v.limit {
			return nil, &RejectError{
				Reason: ReasonLiveLimitExceeded,
				Msg:    "拒绝新增：存活行数已达上限",
			}
		}
	case OpRetract:
		score, ok := v.live[c.Key]
		if !ok {
			return nil, &RejectError{
				Reason: ReasonRetractMissing,
				Msg:    "拒绝撤回：键 " + c.Key + " 当前不存在",
			}
		}
		if score != c.Score {
			return nil, &RejectError{
				Reason: ReasonScoreMismatch,
				Msg:    "拒绝撤回：键 " + c.Key + " 的分数与存活行不符",
			}
		}
	default:
		return nil, &RejectError{
			Reason: ReasonInvalidArgument,
			Msg:    "非法输入：未知操作类型",
		}
	}

	// 2. 记录变更前榜单，并应用变更到存活行集合。
	before := v.top
	if c.Op == OpAdd {
		v.live[c.Key] = c.Score
	} else {
		delete(v.live, c.Key)
	}

	// 3. 依据全部存活行重算前 N 名（榜外存活行同样参与排序以支持补位）。
	after := v.computeTop()

	// 4. 比较前后集合：先离开（按旧名次顺序），再进入（按新名次顺序）。
	entries := diff(before, after)

	// 5. 提交状态与日志。
	v.top = after
	if len(entries) > 0 {
		v.log = append(v.log, entries...)
	}
	return cloneEntries(entries), nil
}

// Snapshot 返回当前前 N 名的快照副本，按名次从 1 开始排序。
func (v *View) Snapshot() []Row {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return cloneRows(v.top)
}

// LiveCount 返回当前存活行数（含榜外行）。
func (v *View) LiveCount() int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return len(v.live)
}

// Log 返回截至目前已产生的全部日志条目的副本，按产生顺序排列。
// 下游按顺序应用这些条目即可始终重建出正确的前 N 名。
func (v *View) Log() []Entry {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return cloneEntries(v.log)
}

// computeTop 依据当前存活行计算排序后的前 N 名。调用方须持有写锁。
func (v *View) computeTop() []Row {
	rows := make([]Row, 0, len(v.live))
	for key, score := range v.live {
		rows = append(rows, Row{Key: key, Score: score})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Score != rows[j].Score {
			return rows[i].Score > rows[j].Score // 分数降序
		}
		return rows[i].Key < rows[j].Key // 分数相同按键字典序升序
	})
	if len(rows) > v.n {
		rows = rows[:v.n]
	}
	return rows
}

// diff 比较变更前后的榜单，返回离开条目（按旧名次）后接进入条目（按新名次）。
func diff(before, after []Row) []Entry {
	afterSet := make(map[string]struct{}, len(after))
	for _, r := range after {
		afterSet[r.Key] = struct{}{}
	}
	beforeSet := make(map[string]struct{}, len(before))
	for _, r := range before {
		beforeSet[r.Key] = struct{}{}
	}

	var entries []Entry
	for rank, r := range before {
		if _, ok := afterSet[r.Key]; !ok {
			entries = append(entries, Entry{
				Kind:  KindLeave,
				Key:   r.Key,
				Score: r.Score,
				Rank:  rank + 1,
			})
		}
	}
	for rank, r := range after {
		if _, ok := beforeSet[r.Key]; !ok {
			entries = append(entries, Entry{
				Kind:  KindEnter,
				Key:   r.Key,
				Score: r.Score,
				Rank:  rank + 1,
			})
		}
	}
	return entries
}

func cloneRows(in []Row) []Row {
	if len(in) == 0 {
		return nil
	}
	out := make([]Row, len(in))
	copy(out, in)
	return out
}

func cloneEntries(in []Entry) []Entry {
	if len(in) == 0 {
		return nil
	}
	out := make([]Entry, len(in))
	copy(out, in)
	return out
}
