// Package role 维护房间成员的角色等级（Owner/Admin/Member）。
package role

// Level 为角色等级，数值越大权限越高。
type Level int

const (
	Member Level = 1
	Admin  Level = 2
	Owner  Level = 3
)

// Table 记录房间内每个成员的等级，房间非空时恰有一名 Owner。
type Table struct {
	roles map[string]Level
	owner string
}

func NewTable() *Table { return &Table{roles: make(map[string]Level)} }

func (t *Table) Clone() *Table {
	c := &Table{roles: make(map[string]Level, len(t.roles)), owner: t.owner}
	for u, l := range t.roles {
		c.roles[u] = l
	}
	return c
}

func (t *Table) Has(u string) bool { _, ok := t.roles[u]; return ok }

func (t *Table) Size() int { return len(t.roles) }

// Level 返回成员等级，不在房间时返回 0。
func (t *Table) Level(u string) Level { return t.roles[u] }

// Join 加入成员：房间为空时成为 Owner，否则为 Member。调用方保证不重复。
func (t *Table) Join(u string) {
	if len(t.roles) == 0 {
		t.roles[u] = Owner
		t.owner = u
		return
	}
	t.roles[u] = Member
}

func (t *Table) Remove(u string) {
	delete(t.roles, u)
	if t.owner == u {
		t.owner = ""
	}
}

// Set 直接设置等级，权限校验由调用方完成。
func (t *Table) Set(u string, l Level) { t.roles[u] = l }

// Transfer 移交 Owner：target 升为 Owner，by 降为 Admin。
func (t *Table) Transfer(by, target string) {
	t.roles[target] = Owner
	t.roles[by] = Admin
	t.owner = target
}

// Roles 返回等级表副本。
func (t *Table) Roles() map[string]Level {
	out := make(map[string]Level, len(t.roles))
	for u, l := range t.roles {
		out[u] = l
	}
	return out
}
