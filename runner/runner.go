package runner

import (
	"errors"
	"sync"

	"ontology/ledger"
	"ontology/script"
)

type ExecFunc func(ver int64) error
type UndoFunc func(ver int64) error

type MigrateResult struct {
	Done []int64
	Fail int64
	More bool
}

type UndoResult struct {
	Undone []int64
	Fail   int64
}

type Engine struct {
	mu       sync.Mutex
	registry *script.Registry
	ledger   *ledger.Ledger
	ooo      bool
	lim      int
	maxNow   int64
	exec     ExecFunc
	undo     UndoFunc
}

func New(ooo bool, lim int, exec ExecFunc, undo UndoFunc) (*Engine, error) {
	if lim < 1 || lim > 1000 || exec == nil || undo == nil {
		return nil, ErrInvalid
	}
	return &Engine{
		registry: script.NewRegistry(),
		ledger:   ledger.New(),
		ooo:      ooo,
		lim:      lim,
		exec:     exec,
		undo:     undo,
	}, nil
}

func (e *Engine) Register(role int, s script.Script) error {
	if s.Ver < 1 || s.Ver > 1_000_000 {
		return ErrInvalid
	}
	if role < 0 || role > 2 {
		return ErrInvalid
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.registry.Register(role, s); err != nil {
		if errors.Is(err, script.ErrPermission) {
			return ErrPermission
		}
		return ErrInvalid
	}
	return nil
}

func (e *Engine) Registered() []script.Script {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.registry.List()
}

func (e *Engine) Ledger() []ledger.Row {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.ledger.Rows()
}

func (e *Engine) Migrate(role int, now int64) (MigrateResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if err := e.validateTimed(role, now, 1); err != nil {
		return MigrateResult{}, err
	}
	rows := e.ledger.Rows()
	if err := failedError(rows); err != nil {
		return MigrateResult{}, err
	}

	registered := e.registry.List()
	latest := make(map[int64]script.Script, len(registered))
	for _, s := range registered {
		latest[s.Ver] = s
	}

	applied := make(map[int64]ledger.Row)
	var maxApplied int64
	for _, row := range rows {
		if row.Status != ledger.StatusSuccess {
			continue
		}
		applied[row.Ver] = row
		if row.Ver > maxApplied {
			maxApplied = row.Ver
		}
	}

	for ver := int64(1); ver <= maxApplied; ver++ {
		row, ok := applied[ver]
		if !ok {
			continue
		}
		if current, ok := latest[ver]; ok && current.Sum != row.Sum {
			return MigrateResult{}, versionError(ErrChecksum, ver)
		}
	}

	pending := make([]script.Script, 0)
	for _, s := range registered {
		if _, ok := applied[s.Ver]; !ok {
			pending = append(pending, s)
		}
	}
	if !e.ooo {
		for _, s := range pending {
			if s.Ver < maxApplied {
				return MigrateResult{}, versionError(ErrOutOfOrder, s.Ver)
			}
		}
	}

	e.maxNow = now
	result := MigrateResult{More: len(pending) > e.lim}
	count := min(len(pending), e.lim)
	for _, s := range pending[:count] {
		if err := safeCall(e.exec, s.Ver); err != nil {
			current, _ := e.registry.Get(s.Ver)
			e.ledger.Append(s.Ver, current.Sum, ledger.StatusFailed)
			result.Fail = s.Ver
			result.More = false
			return result, nil
		}
		current, _ := e.registry.Get(s.Ver)
		e.ledger.Append(s.Ver, current.Sum, ledger.StatusSuccess)
		result.Done = append(result.Done, s.Ver)
	}
	return result, nil
}

func (e *Engine) Repair(role int, now int64) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if err := e.validateTimed(role, now, 2); err != nil {
		return 0, err
	}
	rows := e.ledger.Rows()
	if !hasStatus(rows, ledger.StatusFailed) {
		return 0, ErrNoFailed
	}
	e.maxNow = now
	return e.ledger.RemoveFailed(), nil
}

func (e *Engine) Undo(role int, now int64, to int64) (UndoResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if err := validateCommon(role, now); err != nil {
		return UndoResult{}, err
	}
	if to < 0 || to > 1_000_000 {
		return UndoResult{}, ErrInvalid
	}
	if role < 2 {
		return UndoResult{}, ErrPermission
	}
	if now < e.maxNow {
		return UndoResult{}, ErrNow
	}

	rows := e.ledger.Rows()
	if err := failedError(rows); err != nil {
		return UndoResult{}, err
	}

	selected := make([]ledger.Row, 0)
	for _, row := range rows {
		if row.Status == ledger.StatusSuccess && row.Ver > to {
			selected = append(selected, row)
		}
	}

	var noUndoVer int64
	for _, row := range selected {
		if current, ok := e.registry.Get(row.Ver); !ok || !current.HasUndo {
			if row.Ver > noUndoVer {
				noUndoVer = row.Ver
			}
		}
	}
	if noUndoVer != 0 {
		return UndoResult{}, versionError(ErrNoUndo, noUndoVer)
	}

	e.maxNow = now
	result := UndoResult{}
	for i := len(selected) - 1; i >= 0; i-- {
		row := selected[i]
		if err := safeCall(e.undo, row.Ver); err != nil {
			current, _ := e.registry.Get(row.Ver)
			e.ledger.Append(row.Ver, current.Sum, ledger.StatusFailed)
			result.Fail = row.Ver
			return result, nil
		}
		e.ledger.SetStatus(row.Rank, ledger.StatusUndone)
		result.Undone = append(result.Undone, row.Ver)
	}
	return result, nil
}

func validateCommon(role int, now int64) error {
	if role < 0 || role > 2 || now < 0 || now > 1_000_000_000_000 {
		return ErrInvalid
	}
	return nil
}

func (e *Engine) validateTimed(role int, now int64, required int) error {
	if err := validateCommon(role, now); err != nil {
		return err
	}
	if role < required {
		return ErrPermission
	}
	if now < e.maxNow {
		return ErrNow
	}
	return nil
}

func failedError(rows []ledger.Row) error {
	for _, row := range rows {
		if row.Status == ledger.StatusFailed {
			return versionError(ErrFailed, row.Ver)
		}
	}
	return nil
}

func hasStatus(rows []ledger.Row, status ledger.Status) bool {
	for _, row := range rows {
		if row.Status == status {
			return true
		}
	}
	return false
}

func safeCall(fn func(int64) error, ver int64) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = ErrPanic
		}
	}()
	return fn(ver)
}
