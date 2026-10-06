package vcs

import "bytes"

// Status 普通路径状态分类，含义互斥，每个路径恰好落入一类。
type Status int

const (
	StatusUnchanged             Status = iota // 未变更：三份一致
	StatusStagedAdd                           // 已暂存新增
	StatusStagedModified                      // 已暂存修改
	StatusStagedDeleted                       // 已暂存删除
	StatusWorktreeModified                    // 工作树修改
	StatusWorktreeDeleted                     // 工作树删除
	StatusUntracked                           // 未跟踪
	StatusStagedModifiedAgain                 // 暂存后又修改
	StatusStagedDeleteRecreated               // 暂存删除后又重建
	StatusIgnored                             // 被忽略
	StatusConflict                            // 冲突（优先于普通分类）
)

func (s Status) String() string {
	switch s {
	case StatusUnchanged:
		return "未变更"
	case StatusStagedAdd:
		return "已暂存新增"
	case StatusStagedModified:
		return "已暂存修改"
	case StatusStagedDeleted:
		return "已暂存删除"
	case StatusWorktreeModified:
		return "工作树修改"
	case StatusWorktreeDeleted:
		return "工作树删除"
	case StatusUntracked:
		return "未跟踪"
	case StatusStagedModifiedAgain:
		return "暂存后又修改"
	case StatusStagedDeleteRecreated:
		return "暂存删除后又重建"
	case StatusIgnored:
		return "被忽略"
	case StatusConflict:
		return "冲突"
	default:
		return "未知状态"
	}
}

// ConflictKind 冲突细分。
type ConflictKind int

const (
	ConflictNone          ConflictKind = iota // 非冲突
	ConflictContent                           // 内容冲突
	ConflictDeletedByUs                       // 本方删除对方修改
	ConflictDeletedByThem                     // 本方修改对方删除
	ConflictBothAdded                         // 双方新增
)

func (k ConflictKind) String() string {
	switch k {
	case ConflictNone:
		return "非冲突"
	case ConflictContent:
		return "内容冲突"
	case ConflictDeletedByUs:
		return "本方删除对方修改"
	case ConflictDeletedByThem:
		return "本方修改对方删除"
	case ConflictBothAdded:
		return "双方新增"
	default:
		return "未知冲突"
	}
}

// PathStatus 单路径分类结果。Conflict 仅在 Status == StatusConflict 时有意义。
type PathStatus struct {
	Path     string
	Status   Status
	Conflict ConflictKind
}

// triple 一个路径在三份内容中的取值。缺失以 has* 标记，与空内容区分。
type triple struct {
	snap, idx, wt          []byte
	hasSnap, hasIdx, hasWt bool
}

func eqBytes(a []byte, hasA bool, b []byte, hasB bool) bool {
	return hasA && hasB && bytes.Equal(a, b)
}

// classify 按 (快照,暂存,工作树) 的存在性组合分派，分支互不重叠且覆盖
// 全部 8 种存在性组合，因此分类天然互斥且完备。ignored 仅对未跟踪生效。
func (t triple) classify(ignored bool) Status {
	s, i, w := t.hasSnap, t.hasIdx, t.hasWt
	switch {
	case !s && !i && !w:
		return StatusUnchanged // 路径不存在，调用方另行处理
	case !s && !i: // 仅工作树有
		if ignored {
			return StatusIgnored
		}
		return StatusUntracked
	case !s && i && !w: // 暂存新增后工作树又删除
		return StatusStagedModifiedAgain
	case !s && i && w:
		if eqBytes(t.idx, true, t.wt, true) {
			return StatusStagedAdd
		}
		return StatusStagedModifiedAgain
	case s && !i && !w:
		return StatusStagedDeleted
	case s && !i && w: // 暂存不存在而工作树存在且快照存在
		return StatusStagedDeleteRecreated
	case s && i && !w:
		if eqBytes(t.snap, true, t.idx, true) {
			return StatusWorktreeDeleted
		}
		return StatusStagedModifiedAgain
	default: // s && i && w
		if eqBytes(t.snap, true, t.idx, true) {
			if eqBytes(t.idx, true, t.wt, true) {
				return StatusUnchanged
			}
			return StatusWorktreeModified
		}
		if eqBytes(t.idx, true, t.wt, true) {
			return StatusStagedModified
		}
		return StatusStagedModifiedAgain
	}
}
