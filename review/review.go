package review

// Verdict 是评审裁决。
type Verdict int

const (
	// Approve 批准；RequestChanges 请求变更；Comment 普通评论，不改变先前裁决。
	Approve Verdict = iota + 1
	RequestChanges
	Comment
)

// Status 是检查状态。
type Status int

const (
	Pending Status = iota + 1
	Success
	Failure
	Neutral
	Skipped
)

// Level 是用户权限等级。
type Level int

const (
	None Level = iota
	Write
	Admin
)

// checkKey 定位某 PR 某 head 上某次检查的最后状态。
type checkKey struct {
	pr    int
	check string
	head  int
}

// verdictKey 定位某 PR 某评审人的最近一次非 Comment 裁决。
type verdictKey struct {
	pr   int
	user string
}

// Store 保存全部 PR 的裁决、检查结果与用户权限。
// 调用方（gate）负责加锁；Store 本身无锁。
type Store struct {
	verdicts map[verdictKey]Verdict
	checks   map[checkKey]Status
	perms    map[string]Level

	// Touched 统计 Review/Report 读写的记录条数。
	Touched int
}

// NewStore 创建空账。
func NewStore() *Store {
	return &Store{
		verdicts: make(map[verdictKey]Verdict),
		checks:   make(map[checkKey]Status),
		perms:    make(map[string]Level),
	}
}

// SetVerdict 写入评审人的非 Comment 裁决；Comment 应在调用方过滤。
// 一次写入恰好一条记录，与 PR 总数无关。
func (s *Store) SetVerdict(pr int, user string, v Verdict) {
	s.verdicts[verdictKey{pr, user}] = v
	s.Touched++
}

// ClearVerdict 删除评审人的裁决，返回删除前是否存在。
func (s *Store) ClearVerdict(pr int, user string) bool {
	key := verdictKey{pr, user}
	if _, ok := s.verdicts[key]; !ok {
		return false
	}
	delete(s.verdicts, key)
	s.Touched++
	return true
}

// Verdict 返回评审人裁决与是否存在。
func (s *Store) Verdict(pr int, user string) (Verdict, bool) {
	v, ok := s.verdicts[verdictKey{pr, user}]
	return v, ok
}

// ClearApprovals 清除某 PR 的全部 Approve 裁决，RequestChanges 保留。
// 仅在作者推送（dismissStale）时使用，不计入 touched（非 Review/Report 操作）。
func (s *Store) ClearApprovals(pr int) {
	for key, v := range s.verdicts {
		if key.pr == pr && v == Approve {
			delete(s.verdicts, key)
		}
	}
}

// SetCheck 记录某 (check,head) 的最后状态。
func (s *Store) SetCheck(pr int, check string, head int, st Status) {
	s.checks[checkKey{pr, check, head}] = st
	s.Touched++
}

// Check 返回某 (check,head) 的状态与是否有结果。
func (s *Store) Check(pr int, check string, head int) (Status, bool) {
	st, ok := s.checks[checkKey{pr, check, head}]
	return st, ok
}

// SetLevel 设置用户权限等级。
func (s *Store) SetLevel(user string, level Level) { s.perms[user] = level }

// Level 返回用户权限等级，未登记者为 None。
func (s *Store) Level(user string) Level { return s.perms[user] }

// ActiveVerdicts 返回某 PR 满足条件的评审人集合：
// 评审人当前权限为 write/admin，且最近一次非 Comment 裁决等于 v。
// 供 gate 判定时使用（判定时刻权限），不增加 touched。
func (s *Store) ActiveVerdicts(pr int, v Verdict) map[string]struct{} {
	out := make(map[string]struct{})
	for key, got := range s.verdicts {
		if key.pr != pr || got != v {
			continue
		}
		level := s.perms[key.user]
		if level == Write || level == Admin {
			out[key.user] = struct{}{}
		}
	}
	return out
}

// resetTouched 仅供测试使用。
func (s *Store) resetTouched() { s.Touched = 0 }
