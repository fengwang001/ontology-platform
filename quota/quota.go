// Package quota 维护租户配额与预留账本：已提交用量 U、预留量 R、
// 额度 q、开启中会话数，以及同键对象大小表。账本不感知时钟与日志。
package quota

import "errors"

// MaxQuota 是租户额度的上界（含）。
const MaxQuota = int64(1_000_000_000_000_000) // 1e15

var (
	// ErrInvalidParam 表示参数非法（空租户名或 q 越界）。
	ErrInvalidParam = errors.New("quota: 参数非法")
	// ErrBelowUsed 表示降额低于当前占用（q < U+R）。
	ErrBelowUsed = errors.New("quota: 低于占用")
	// ErrQuotaExceeded 表示额度不足（U+R+新增 > q）。
	ErrQuotaExceeded = errors.New("quota: 额度不足")
)

// TenantView 是租户账本的只读视图。
type TenantView struct {
	Quota    int64
	Used     int64
	Reserved int64
	Open     int
	Objects  map[string]int64
}

type tenant struct {
	quota    int64
	used     int64
	reserved int64
	open     int
	objects  map[string]int64
}

// Ledger 是租户配额账本。调用方需自行串行化。
type Ledger struct {
	tenants map[string]*tenant
}

// NewLedger 返回空账本。
func NewLedger() *Ledger { return &Ledger{tenants: map[string]*tenant{}} }

func (l *Ledger) get(t string) *tenant {
	tn, ok := l.tenants[t]
	if !ok {
		tn = &tenant{objects: map[string]int64{}}
		l.tenants[t] = tn
	}
	return tn
}

// CheckSetQuota 校验 SetQuota 的参数与「不低于占用」约束，不修改账本。
func (l *Ledger) CheckSetQuota(t string, q int64) error {
	if t == "" || q < 0 || q > MaxQuota {
		return ErrInvalidParam
	}
	tn, ok := l.tenants[t]
	if !ok {
		return nil
	}
	if q < tn.used+tn.reserved {
		return ErrBelowUsed
	}
	return nil
}

// ApplySetQuota 落地 SetQuota；调用前须通过 CheckSetQuota。
func (l *Ledger) ApplySetQuota(t string, q int64) {
	l.get(t).quota = q
}

// View 返回租户当前视图；未出现过的租户返回零值（q=0）。
func (l *Ledger) View(t string) TenantView {
	tn, ok := l.tenants[t]
	if !ok {
		return TenantView{Objects: map[string]int64{}}
	}
	objects := make(map[string]int64, len(tn.objects))
	for k, v := range tn.objects {
		objects[k] = v
	}
	return TenantView{
		Quota:    tn.quota,
		Used:     tn.used,
		Reserved: tn.reserved,
		Open:     tn.open,
		Objects:  objects,
	}
}

// Reserve 为一次新会话全量预留 total，并计一个开启中会话。
func (l *Ledger) Reserve(t string, total int64) {
	tn := l.get(t)
	tn.reserved += total
	tn.open++
}

// Release 释放一个会话的预留（中止或到期回收）。
func (l *Ledger) Release(t string, total int64) {
	tn := l.get(t)
	tn.reserved -= total
	tn.open--
}

// Commit 完成会话：预留转用量，并按同键旧对象大小冲正 U。
func (l *Ledger) Commit(t, key string, total int64) {
	tn := l.get(t)
	replaced := tn.objects[key]
	tn.reserved -= total
	tn.used += total - replaced
	tn.open--
	tn.objects[key] = total
}

// Tenants 返回账本中出现过的租户名。
func (l *Ledger) Tenants() []string {
	names := make([]string, 0, len(l.tenants))
	for name := range l.tenants {
		names = append(names, name)
	}
	return names
}
