// Package grant 实现带事后评审的紧急访问授权服务。
package grant

import "errors"

// 拒绝原因。各操作按文档规定的顺序只报告第一个命中的错误。
var (
	ErrParam      = errors.New("grant: 参数非法")
	ErrClock      = errors.New("grant: 时钟回退")
	ErrDuplicate  = errors.New("grant: 重复")
	ErrNotFound   = errors.New("grant: 未登记或不存在")
	ErrLocked     = errors.New("grant: 申请人被锁定")
	ErrTooMany    = errors.New("grant: 并发授权过多")
	ErrSelfReview = errors.New("grant: 自评")
	ErrPermission = errors.New("grant: 权限不足")
	ErrDecided    = errors.New("grant: 授权已决")
	ErrOverdue    = errors.New("grant: 评审逾期")
	ErrFuture     = errors.New("grant: 未来时刻")
)

// Grant 是一条紧急访问授权。Rw/Lk/PolicyVer 为请求时刻的策略快照，
// 之后的 SetPolicy 不影响本授权。ApprovedAt/RejectedAt 为 -1 表示无。
type Grant struct {
	ID         string
	Requester  string
	Resource   string
	Start      int64
	NominalEnd int64 // Start + D
	Rw         int64 // 快照：评审窗口
	Lk         int64 // 快照：锁定时长
	PolicyVer  int64 // 快照：策略版本
	ApprovedAt int64
	RejectedAt int64
}

func (g *Grant) approved() bool { return g.ApprovedAt >= 0 }
func (g *Grant) rejected() bool { return g.RejectedAt >= 0 }
func (g *Grant) decided() bool  { return g.approved() || g.rejected() }

// EffEnd 返回有效结束时刻：驳回取 min(nominalEnd, rejectedAt)，
// 批准取 nominalEnd，无评审取 min(nominalEnd, start+Rw)。
// 恒有 Start ≤ EffEnd ≤ NominalEnd。
func (g *Grant) EffEnd() int64 {
	switch {
	case g.rejected():
		return min(g.NominalEnd, g.RejectedAt)
	case g.approved():
		return g.NominalEnd
	default:
		return min(g.NominalEnd, g.Start+g.Rw)
	}
}

// lockStart 返回锁定起点与是否存在锁定：被驳回者为驳回时刻，
// 无任何评审者为 start+Rw，已批准者无锁定。
func (g *Grant) lockStart() (int64, bool) {
	switch {
	case g.rejected():
		return g.RejectedAt, true
	case g.approved():
		return 0, false
	default:
		return g.Start + g.Rw, true
	}
}

// lockedAt 报告本授权是否在时刻 now 锁定其申请人：
// lockStart ≤ now < lockStart+Lk（Lk 为 0 时区间为空，永不锁定）。
func (g *Grant) lockedAt(now int64) bool {
	ls, ok := g.lockStart()
	return ok && ls <= now && now < ls+g.Lk
}

// activeAt 报告本授权在时刻 t 是否处于有效区间 [Start, EffEnd)。
func (g *Grant) activeAt(t int64) bool {
	return g.Start <= t && t < g.EffEnd()
}
