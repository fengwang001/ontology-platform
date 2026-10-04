// Package runner 提供迁移引擎：权限、校验与执行。
package runner

import (
	"errors"
	"sort"
	"sync"

	"ontology/ledger"
	"ontology/script"
)

// MaxNow 是 now 参数的合法上界（含）。
const MaxNow = 1_000_000_000_000

// 角色。
const (
	RoleReadOnly = 0
	RoleMigrator = 1
	RoleAdmin    = 2
)

// 拒绝原因哨兵，可用 errors.Is 判定。
var (
	ErrArgument      = errors.New("runner: invalid argument")
	ErrPermission    = errors.New("runner: permission denied")
	ErrNowRegression = errors.New("runner: now regression")
	ErrFailed        = errors.New("runner: ledger contains failed row")
	ErrChecksum      = errors.New("runner: checksum mismatch")
	ErrOutOfOrder    = errors.New("runner: out-of-order script")
	ErrNoFailed      = errors.New("runner: no failed row")
	ErrNoUndo        = errors.New("runner: script has no undo")
)

// Reject 描述一次被拒绝的操作及其判定依据。
type Reject struct {
	Err error  // 上述哨兵之一
	Ver uint32 // 相关脚本版本（无则为 0）
}

// Error 实现 error。
func (r *Reject) Error() string {
	if r.Ver == 0 {
		return r.Err.Error()
	}
	return r.Err.Error() + " (ver=" + itoa(r.Ver) + ")"
}

// Unwrap 暴露哨兵供 errors.Is 判定。
func (r *Reject) Unwrap() error { return r.Err }

func itoa(v uint32) string {
	if v == 0 {
		return "0"
	}
	var buf [10]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

// Engine 是版本化迁移引擎。全部方法可并发调用，
// 结果等价于某个串行顺序（回调在持有引擎锁时执行，调用方保证回调不重入引擎）。
type Engine struct {
	mu     sync.Mutex
	ooo    bool
	lim    int
	exec   func(ver uint32) error
	undo   func(ver uint32) error
	reg    *script.Registry
	led    *ledger.Ledger
	maxNow uint64
}

// New 构造引擎。ooo 为是否允许乱序补执行，lim 为单次 Migrate 最多执行的脚本数（1..1000）。
func New(ooo bool, lim int, execFn, undoFn func(ver uint32) error) (*Engine, error) {
	if lim < 1 || lim > 1000 {
		return nil, &Reject{Err: ErrArgument}
	}
	if execFn == nil || undoFn == nil {
		return nil, &Reject{Err: ErrArgument}
	}
	return &Engine{
		ooo:  ooo,
		lim:  lim,
		exec: execFn,
		undo: undoFn,
		reg:  script.NewRegistry(),
		led:  ledger.New(),
	}, nil
}

// validRole 报告 role 是否是已定义的角色。
func validRole(role int) bool {
	return role == RoleReadOnly || role == RoleMigrator || role == RoleAdmin
}

// safeCall 调用回调，panic 视同返回错误。
func safeCall(fn func(ver uint32) error, ver uint32) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errPanic{v: r}
		}
	}()
	return fn(ver)
}

type errPanic struct{ v any }

func (p errPanic) Error() string { return "runner: callback panic" }

// Register 登记脚本（role >= 1），同 ver 覆盖，不改账本，不带 now。
func (e *Engine) Register(role int, s script.Script) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !s.Valid() || !validRole(role) {
		return &Reject{Err: ErrArgument}
	}
	if role < RoleMigrator {
		return &Reject{Err: ErrPermission}
	}
	e.reg.Upsert(s)
	return nil
}

// Ledger 返回账本深拷贝（只读查询）。
func (e *Engine) Ledger() []ledger.Row {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.led.Rows()
}

// Registered 返回登记表深拷贝（只读查询，按 ver 升序）。
func (e *Engine) Registered() []script.Script {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.reg.Snapshot()
}

// Migrate 按 ver 升序执行未应用的脚本，至多 lim 个。
// 成功追加 S 行；失败追加一条 F 行并立即停止（此前 S 行保留，逐脚本提交）。
// 执行失败属于正常返回：err 为 nil，failedVer 为失败的 ver。
func (e *Engine) Migrate(role int, now uint64) (applied []uint32, failedVer uint32, more bool, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !validRole(role) || now > MaxNow {
		return nil, 0, false, &Reject{Err: ErrArgument}
	}
	if role < RoleMigrator {
		return nil, 0, false, &Reject{Err: ErrPermission}
	}
	if now < e.maxNow {
		return nil, 0, false, &Reject{Err: ErrNowRegression}
	}
	if row, ok := e.led.FirstFailed(); ok {
		return nil, 0, false, &Reject{Err: ErrFailed, Ver: row.Ver}
	}
	appliedRows := e.led.Applied()
	sort.Slice(appliedRows, func(i, j int) bool { return appliedRows[i].Ver < appliedRows[j].Ver })
	for _, row := range appliedRows {
		s, _ := e.reg.Get(row.Ver)
		if s.Sum != row.Sum {
			return nil, 0, false, &Reject{Err: ErrChecksum, Ver: row.Ver}
		}
	}
	inA := make(map[uint32]bool, len(appliedRows))
	for _, row := range appliedRows {
		inA[row.Ver] = true
	}
	if !e.ooo {
		maxApplied := e.led.MaxAppliedVer()
		for _, s := range e.reg.Snapshot() {
			if s.Ver < maxApplied && !inA[s.Ver] {
				return nil, 0, false, &Reject{Err: ErrOutOfOrder, Ver: s.Ver}
			}
		}
	}
	e.maxNow = now
	applied = []uint32{}
	for _, s := range e.reg.Snapshot() {
		if inA[s.Ver] {
			continue
		}
		if len(applied) >= e.lim {
			return applied, 0, true, nil
		}
		if callErr := safeCall(e.exec, s.Ver); callErr != nil {
			e.led.Append(s.Ver, s.Sum, ledger.StatusFailed)
			return applied, s.Ver, false, nil
		}
		e.led.Append(s.Ver, s.Sum, ledger.StatusSuccess)
		applied = append(applied, s.Ver)
	}
	return applied, 0, false, nil
}

// Repair 删除全部 F 行（role 须为管理员），返回删除数。
func (e *Engine) Repair(role int, now uint64) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !validRole(role) || now > MaxNow {
		return 0, &Reject{Err: ErrArgument}
	}
	if role != RoleAdmin {
		return 0, &Reject{Err: ErrPermission}
	}
	if now < e.maxNow {
		return 0, &Reject{Err: ErrNowRegression}
	}
	if _, ok := e.led.FirstFailed(); !ok {
		return 0, &Reject{Err: ErrNoFailed}
	}
	e.maxNow = now
	return e.led.RemoveFailed(), nil
}

// Undo 按 rank 降序回退 A 中 ver 大于 to 的行（role 须为管理员）。
// 先检查全部选中行的 hasUndo（报最大 ver），通过后逐行回退；
// 回退失败追加 F 行并停止，已改为 U 的保留。Undo 不受 lim 限制。
func (e *Engine) Undo(role int, now uint64, to uint32) (undone []uint32, failedVer uint32, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !validRole(role) || now > MaxNow || to > script.MaxVer {
		return nil, 0, &Reject{Err: ErrArgument}
	}
	if role != RoleAdmin {
		return nil, 0, &Reject{Err: ErrPermission}
	}
	if now < e.maxNow {
		return nil, 0, &Reject{Err: ErrNowRegression}
	}
	if row, ok := e.led.FirstFailed(); ok {
		return nil, 0, &Reject{Err: ErrFailed, Ver: row.Ver}
	}
	var selected []ledger.Row
	for _, row := range e.led.Applied() {
		if row.Ver > to {
			selected = append(selected, row)
		}
	}
	var maxNoUndo uint32
	for _, row := range selected {
		s, _ := e.reg.Get(row.Ver)
		if !s.HasUndo && row.Ver > maxNoUndo {
			maxNoUndo = row.Ver
		}
	}
	if maxNoUndo != 0 {
		return nil, 0, &Reject{Err: ErrNoUndo, Ver: maxNoUndo}
	}
	e.maxNow = now
	sort.Slice(selected, func(i, j int) bool { return selected[i].Rank > selected[j].Rank })
	undone = []uint32{}
	for _, row := range selected {
		s, _ := e.reg.Get(row.Ver)
		if callErr := safeCall(e.undo, row.Ver); callErr != nil {
			e.led.Append(row.Ver, s.Sum, ledger.StatusFailed)
			return undone, row.Ver, nil
		}
		e.led.MarkUndone(row.Rank)
		undone = append(undone, row.Ver)
	}
	return undone, 0, nil
}
