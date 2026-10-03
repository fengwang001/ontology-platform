// Package grant 定义授权实体及其有效区间、锁定的纯函数推导。
package grant

// None 表示评审时刻尚未记录。
const None = -1

// Grant 是一次紧急访问授权，策略字段为请求时刻的快照。
type Grant struct {
	ID         string
	Requester  string
	Resource   string
	Start      int64
	NominalEnd int64
	Rw         int64
	Lk         int64
	PolicyVer  int64
	ApprovedAt int64
	RejectedAt int64
}

// New 创建无评审记录的授权。
func New(id, requester, resource string, start, d, rw, lk, ver int64) *Grant {
	return &Grant{
		ID:         id,
		Requester:  requester,
		Resource:   resource,
		Start:      start,
		NominalEnd: start + d,
		Rw:         rw,
		Lk:         lk,
		PolicyVer:  ver,
		ApprovedAt: None,
		RejectedAt: None,
	}
}

// Reviewed 报告授权是否已有评审结论。
func (g *Grant) Reviewed() bool { return g.ApprovedAt != None || g.RejectedAt != None }

// Approve 记录批准时刻。
func (g *Grant) Approve(now int64) { g.ApprovedAt = now }

// Reject 记录驳回时刻。
func (g *Grant) Reject(now int64) { g.RejectedAt = now }

// EffEnd 返回有效结束时刻（半开区间右端）。
// 恒有 Start <= EffEnd <= NominalEnd。
func (g *Grant) EffEnd() int64 {
	if g.RejectedAt != None {
		return min(g.NominalEnd, g.RejectedAt)
	}
	if g.ApprovedAt != None {
		return g.NominalEnd
	}
	return min(g.NominalEnd, g.Start+g.Rw)
}

// LockStart 返回锁定起点；已批准的授权无锁定。
func (g *Grant) LockStart() (int64, bool) {
	if g.RejectedAt != None {
		return g.RejectedAt, true
	}
	if g.ApprovedAt != None {
		return 0, false
	}
	return g.Start + g.Rw, true
}
