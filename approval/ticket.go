// Package approval 管理单个部署请求的审批票据与有效期。
package approval

import "errors"

var (
	// ErrSelfApproval 请求者批准自己发起的请求。
	ErrSelfApproval = errors.New("approval: self approval is forbidden")
	// ErrDuplicateApproval 同一批准人仍持有有效批准时再次批准。
	ErrDuplicateApproval = errors.New("approval: duplicate active approval")
)

// Ticket 是一条部署请求的审批票据。
// 每个批准人只保留最近一次批准时刻；旧记录被新记录替换。
type Ticket struct {
	ttl   int64
	votes map[string]int64
}

// New 创建有效期为 ttl 秒的空票据。
func New(ttl int64) *Ticket {
	return &Ticket{ttl: ttl, votes: make(map[string]int64)}
}

// Add 记录 approver 在 at 时刻的一次批准。
// 若该批准人已有在 at 时刻仍有效的批准，返回 ErrDuplicateApproval；
// 旧批准已失效时以 at 替换旧时刻。
func (t *Ticket) Add(approver string, at int64) error {
	if prev, ok := t.votes[approver]; ok && at < prev+t.ttl {
		return ErrDuplicateApproval
	}
	t.votes[approver] = at
	return nil
}

// ValidAt 返回时刻 now 仍有效的（不同）批准人数。
// 有效当且仅当 now < at+ttl，恰等即失效。
func (t *Ticket) ValidAt(now int64) int {
	n := 0
	for _, at := range t.votes {
		if now < at+t.ttl {
			n++
		}
	}
	return n
}

// HasValid 报告 approver 在 now 时刻是否仍持有有效批准。
func (t *Ticket) HasValid(approver string, now int64) bool {
	at, ok := t.votes[approver]
	return ok && now < at+t.ttl
}
