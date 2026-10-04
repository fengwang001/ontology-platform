// Package party 维护组队登记表：玩家名唯一归属一个组队。
package party

import "errors"

var (
	// ErrInvalidParam 参数非法。
	ErrInvalidParam = errors.New("party: invalid param")
	// ErrPartyExists 组队 id 已存在。
	ErrPartyExists = errors.New("party: party exists")
	// ErrMemberBusy 某成员已属于其他组队。
	ErrMemberBusy = errors.New("party: member already in a party")
)

// Party 是一支已登记组队，Members 保留登记次序。
type Party struct {
	ID      string
	Members []string
}

// Book 为组队登记簿。
type Book struct {
	parties map[string]*Party
	owner   map[string]string
}

// NewBook 创建空登记簿。
func NewBook() *Book {
	return &Book{parties: map[string]*Party{}, owner: map[string]string{}}
}

// Form 登记一支组队。拒绝次序：参数非法 > pid 已存在 > 成员已属组队。
func (b *Book) Form(pid string, members []string) (*Party, error) {
	if pid == "" || len(members) < 1 || len(members) > 16 {
		return nil, ErrInvalidParam
	}
	for _, name := range members {
		if name == "" {
			return nil, ErrInvalidParam
		}
	}
	seen := make(map[string]struct{}, len(members))
	for _, name := range members {
		if _, dup := seen[name]; dup {
			return nil, ErrInvalidParam
		}
		seen[name] = struct{}{}
	}
	if _, exists := b.parties[pid]; exists {
		return nil, ErrPartyExists
	}
	for _, name := range members {
		if other, busy := b.owner[name]; busy && other != pid {
			return nil, ErrMemberBusy
		}
	}
	cp := make([]string, len(members))
	copy(cp, members)
	p := &Party{ID: pid, Members: cp}
	b.parties[pid] = p
	for _, name := range members {
		b.owner[name] = pid
	}
	return p, nil
}

// Get 按 id 查询组队，返回成员切片的副本。
func (b *Book) Get(pid string) (*Party, bool) {
	p, ok := b.parties[pid]
	if !ok {
		return nil, false
	}
	cp := make([]string, len(p.Members))
	copy(cp, p.Members)
	return &Party{ID: p.ID, Members: cp}, true
}
