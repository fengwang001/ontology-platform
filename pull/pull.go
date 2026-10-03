// Package pull 在复合游标之上分页拉取源端变更，并原子地交给 sink 应用。
package pull

import (
	"errors"
	"sync"

	"ontology/cursor"
	"ontology/sink"
)

// ErrSource 表示某一页 Query 返回错误，本轮拉取作废且状态零改动。
var ErrSource = errors.New("pull: source query error")

// ErrInvalidParam 表示构造参数、now 或源端返回行不合法。
var ErrInvalidParam = errors.New("pull: invalid parameter")

// ErrClockRollback 表示 Pull 的 now 小于先前已接受的最大 now。
var ErrClockRollback = errors.New("pull: clock rollback")

// ErrForbidden 表示 SetSchema 的 role 不等于 2。
var ErrForbidden = errors.New("pull: set schema forbidden for role")

// ErrDowngrade 表示 SetSchema 试图降低 schema 版本。
var ErrDowngrade = errors.New("pull: schema version downgrade")

// 参数上界（与规格一致）。
const (
	maxDelay = int64(1_000_000_000)
	maxID    = int64(1_000_000_000)
	maxVer   = int64(1_000_000_000)
	maxSv    = int64(100)
	maxLimit = int64(1000)
)

// Row 是源端返回的一行：(id, ts, ver, sv)。
type Row struct {
	ID  int64
	Ts  int64
	Ver int64
	Sv  int64
}

// Source 是调用方注入的源端接口。
// 返回满足 (ts,id) > after、ts <= maxTs、commitAt <= visibleAt 的行，
// 按 (ts,id) 升序，至多 limit 行；每个 id 至多其可见最新版本一行。
type Source interface {
	Query(afterTs, afterID, limit, maxTs, visibleAt int64) (rows []Row, err error)
}

// Result 是一次 Pull 的结果。
type Result struct {
	Applied int
	Dup     int
	NewDLQ  int
	Queries int
}

// Puller 组合 cursor 状态机与 sink，串行化所有操作。
type Puller struct {
	mu sync.Mutex

	src   Source
	d     int64
	b     int64
	limit int64

	tracker *cursor.Tracker
	sink    *sink.Sink
}

// New 以 D、B、limit、S 构造拉取器；参数越界返回 ErrInvalidParam。
func New(src Source, d, b, limit, s int64) (*Puller, error) {
	if src == nil || d < 0 || d > maxDelay || b < 0 || b > maxDelay ||
		limit < 1 || limit > maxLimit || s < 1 || s > maxSv {
		return nil, ErrInvalidParam
	}
	return &Puller{
		src:     src,
		d:       d,
		b:       b,
		limit:   limit,
		tracker: cursor.New(),
		sink:    sink.New(s),
	}, nil
}

// Pull 执行一轮增量拉取与原子应用。
//
// 整个操作持有拉取器写锁：所有 Pull/SetSchema 严格串行，结果等价于某个串行顺序；
// Query 期间不修改任何状态，任一页出错或行非法时在应用前返回，保证零改动。
func (p *Puller) Pull(now int64) (Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	hz, start, skip, err := p.tracker.BeginPull(now, p.d, p.b)
	if err != nil {
		return Result{}, mapCursorErr(err)
	}
	if skip {
		// hz<0：本轮不查询，成功返回空结果，但仍推进最大 now。
		p.tracker.Commit(now, nil)
		return Result{Queries: 0}, nil
	}

	rows, queries, err := p.fetchAll(start, hz, now)
	if err != nil {
		return Result{Queries: queries}, err
	}

	out := p.sink.Apply(toSinkRows(rows))

	var last *cursor.Pos
	if n := len(rows); n > 0 {
		lp := cursor.Pos{Ts: rows[n-1].Ts, ID: rows[n-1].ID}
		last = &lp
	}
	p.tracker.Commit(now, last)

	return Result{
		Applied: out.Applied,
		Dup:     out.Dup,
		NewDLQ:  out.NewDLQ,
		Queries: queries,
	}, nil
}

// fetchAll 分页读取本轮全部行并逐页校验；不在此处修改任何状态。
func (p *Puller) fetchAll(start cursor.Pos, hz, now int64) ([]Row, int, error) {
	var rows []Row
	after := start
	queries := 0
	for {
		page, qerr := p.src.Query(after.Ts, after.ID, p.limit, hz, now)
		queries++
		if qerr != nil {
			return nil, queries, ErrSource
		}
		if int64(len(page)) > p.limit {
			return nil, queries, ErrInvalidParam
		}
		if err := validatePage(page, after, hz); err != nil {
			return nil, queries, err
		}
		rows = append(rows, page...)
		if int64(len(page)) < p.limit {
			return rows, queries, nil
		}
		last := page[len(page)-1]
		after = cursor.Pos{Ts: last.Ts, ID: last.ID}
	}
}

// validatePage 校验一页行：取值范围、ts<=maxTs、相对 after 与页内 (ts,id) 严格升序。
func validatePage(page []Row, after cursor.Pos, hz int64) error {
	prev := after
	for _, r := range page {
		if r.ID < 1 || r.ID > maxID ||
			r.Ts < 0 || r.Ts > cursor.MaxNow ||
			r.Ver < 1 || r.Ver > maxVer ||
			r.Sv < 1 || r.Sv > maxSv {
			return ErrInvalidParam
		}
		if r.Ts > hz {
			return ErrInvalidParam
		}
		cur := cursor.Pos{Ts: r.Ts, ID: r.ID}
		if !prev.Less(cur) {
			return ErrInvalidParam
		}
		prev = cur
	}
	return nil
}

// SetSchema 修改 sink 的 schema 上限（role 必须为 2）；只改 S，不重放死信。
func (p *Puller) SetSchema(role, ns int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return mapSinkErr(p.sink.SetSchema(role, ns))
}

// Cur 返回当前游标 (ts,id)。
func (p *Puller) Cur() (ts, id int64) {
	pos := p.tracker.Cur()
	return pos.Ts, pos.ID
}

// Applied 返回 id 已应用的最大版本。
func (p *Puller) Applied(id int64) int64 { return p.sink.Applied(id) }

// DLQ 查询死信中首次进入的行。
func (p *Puller) DLQ(id, ver int64) (Row, bool) {
	r, ok := p.sink.DLQ(sink.Key{ID: id, Ver: ver})
	return fromSinkRow(r), ok
}

// DLQSize 返回死信条目数。
func (p *Puller) DLQSize() int { return p.sink.DLQSize() }

// Schema 返回当前 schema 上限。
func (p *Puller) Schema() int64 { return p.sink.Schema() }

// MaxNow 返回已接受 Pull 的最大 now。
func (p *Puller) MaxNow() int64 { return p.tracker.MaxNow() }

func toSinkRows(rows []Row) []sink.Row {
	out := make([]sink.Row, len(rows))
	for i, r := range rows {
		out[i] = sink.Row{ID: r.ID, Ts: r.Ts, Ver: r.Ver, Sv: r.Sv}
	}
	return out
}

func fromSinkRow(r sink.Row) Row {
	return Row{ID: r.ID, Ts: r.Ts, Ver: r.Ver, Sv: r.Sv}
}

func mapCursorErr(err error) error {
	switch {
	case errors.Is(err, cursor.ErrInvalidParam):
		return ErrInvalidParam
	case errors.Is(err, cursor.ErrClockRollback):
		return ErrClockRollback
	default:
		return err
	}
}

func mapSinkErr(err error) error {
	switch {
	case errors.Is(err, sink.ErrInvalidParam):
		return ErrInvalidParam
	case errors.Is(err, sink.ErrForbidden):
		return ErrForbidden
	case errors.Is(err, sink.ErrDowngrade):
		return ErrDowngrade
	default:
		return err
	}
}
