// Package pull 实现基于更新时间戳复合游标的增量拉取器：
// 分页拉取源端变更，用回看窗口补回晚提交的行，整轮读完后原子应用。
package pull

import (
	"errors"
	"fmt"
	"sync"

	"ontology/cursor"
	"ontology/sink"
)

// 取值范围常量。
const (
	MaxNow   int64 = 1_000_000_000_000 // now 与 ts 的上界 10^12
	MaxID    int64 = 1_000_000_000     // id 上界 10^9
	MaxVer   int64 = 1_000_000_000     // ver 上界 10^9
	MaxDelay int64 = 1_000_000_000     // D、B 上界 10^9
	MaxLimit int64 = 1000              // limit 上界
	MaxS     int64 = 100               // S 上界
)

// 错误。
var (
	ErrInvalid   = errors.New("pull: 参数非法")
	ErrClock     = errors.New("pull: 时钟回退")
	ErrPerm      = errors.New("pull: SetSchema 权限不足")
	ErrDowngrade = errors.New("pull: SetSchema 不允许降级")
	ErrSource    = errors.New("pull: 源端查询错误")
)

// Source 是调用方注入的源端接口。
// Query 返回满足 (ts,id) 字典序大于 after、ts 不大于 maxTs、
// 提交可见时刻 commitAt 不大于 visibleAt 的行，按 (ts,id) 升序至多 limit 行；
// 每个 id 至多返回其可见的最新版本一行。
type Source interface {
	Query(after cursor.Cursor, limit int, maxTS, visibleAt int64) ([]sink.Row, error)
}

// Stats 是一轮 Pull 的结果。
type Stats struct {
	Applied int // 应用数
	Dup     int // 重复数
	Dead    int // 新入死信数
	Queries int // Query 调用次数
}

// Puller 是增量拉取器。所有方法可并发调用，效果等价于某个串行顺序。
type Puller struct {
	mu     sync.Mutex
	src    Source
	D      int64 // 稳定延迟
	B      int64 // 回看长度
	limit  int   // 每页行数上限
	S      int64 // sink 支持的最高 schema 版本
	cur    cursor.Cursor
	snk    *sink.Sink
	maxNow int64
}

// New 构造 Puller。参数越界返回 ErrInvalid。
func New(src Source, D, B, limit, S int64) (*Puller, error) {
	if src == nil ||
		D < 0 || D > MaxDelay ||
		B < 0 || B > MaxDelay ||
		limit < 1 || limit > MaxLimit ||
		S < 1 || S > MaxS {
		return nil, fmt.Errorf("%w: D=%d B=%d limit=%d S=%d", ErrInvalid, D, B, limit, S)
	}
	return &Puller{
		src:   src,
		D:     D,
		B:     B,
		limit: int(limit),
		S:     S,
		snk:   sink.New(),
	}, nil
}

// Pull 执行一轮增量拉取。
//
// 拒绝次序：参数非法（now 越界或源端行非法）→ 时钟回退 → ErrSource。
// 任何拒绝或源端错误都不改变游标、applied、死信与最大 now。
func (p *Puller) Pull(now int64) (Stats, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if now < 0 || now > MaxNow {
		return Stats{}, fmt.Errorf("%w: now=%d", ErrInvalid, now)
	}
	if now < p.maxNow {
		return Stats{}, fmt.Errorf("%w: now=%d < maxNow=%d", ErrClock, now, p.maxNow)
	}

	hz := now - p.D
	if hz < 0 {
		// 本轮不查询，成功返回空且推进最大 now。
		p.maxNow = now
		return Stats{}, nil
	}

	// 整轮读完再应用：读取期间不改任何状态。
	var rows []sink.Row
	queries := 0
	after := cursor.LookbackStart(p.cur, p.B)
	for {
		page, err := p.src.Query(after, p.limit, hz, now)
		queries++
		if err != nil {
			return Stats{}, fmt.Errorf("%w: %v", ErrSource, err)
		}
		if err := validatePage(page, p.limit, hz, after); err != nil {
			return Stats{}, err
		}
		rows = append(rows, page...)
		if len(page) < p.limit {
			break
		}
		after = cursor.Cursor{TS: page[len(page)-1].TS, ID: page[len(page)-1].ID}
	}

	// 原子应用与游标更新。
	var st Stats
	st.Queries = queries
	for _, r := range rows {
		switch p.snk.Apply(r, p.S) {
		case sink.Applied:
			st.Applied++
		case sink.Dup:
			st.Dup++
		case sink.DeadNew:
			st.Dead++
		case sink.DeadDup:
			// 已在死信中，忽略，不计数。
		}
	}
	if n := len(rows); n > 0 {
		p.cur = cursor.Max(p.cur, cursor.Cursor{TS: rows[n-1].TS, ID: rows[n-1].ID})
	}
	p.maxNow = now
	return st, nil
}

// SetSchema 将 sink 支持的最高 schema 版本提升为 s。
// 仅 role==2 有权调用；s 不得小于当前 S。不重放死信。
func (p *Puller) SetSchema(role, s int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if s < 1 || s > MaxS {
		return fmt.Errorf("%w: S'=%d", ErrInvalid, s)
	}
	if role != 2 {
		return fmt.Errorf("%w: role=%d", ErrPerm, role)
	}
	if s < p.S {
		return fmt.Errorf("%w: S'=%d < S=%d", ErrDowngrade, s, p.S)
	}
	p.S = s
	return nil
}

// Snapshot 返回当前状态的一致性快照。
type Snapshot struct {
	Cur     cursor.Cursor
	Applied map[int64]int64
	Dead    map[sink.Key]sink.Row
	MaxNow  int64
	S       int64
}

// Snapshot 返回当前状态副本。
func (p *Puller) Snapshot() Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	return Snapshot{
		Cur:     p.cur,
		Applied: p.snk.AppliedMap(),
		Dead:    p.snk.DeadMap(),
		MaxNow:  p.maxNow,
		S:       p.S,
	}
}

// validatePage 校验一页源端数据；任何违规都按参数非法拒绝。
func validatePage(page []sink.Row, limit int, maxTS int64, after cursor.Cursor) error {
	if len(page) > limit {
		return fmt.Errorf("%w: 页行数 %d 超过 limit %d", ErrInvalid, len(page), limit)
	}
	prev := after
	for i, r := range page {
		if r.ID < 1 || r.ID > MaxID ||
			r.TS < 0 || r.TS > MaxNow ||
			r.Ver < 1 || r.Ver > MaxVer ||
			r.SV < 1 {
			return fmt.Errorf("%w: 第 %d 行取值越界 %+v", ErrInvalid, i, r)
		}
		if r.TS > maxTS {
			return fmt.Errorf("%w: 第 %d 行 ts=%d 大于 maxTs=%d", ErrInvalid, i, r.TS, maxTS)
		}
		c := cursor.Cursor{TS: r.TS, ID: r.ID}
		if !c.After(prev) {
			return fmt.Errorf("%w: 第 %d 行 (%d,%d) 不大于前序 (%d,%d)",
				ErrInvalid, i, r.TS, r.ID, prev.TS, prev.ID)
		}
		prev = c
	}
	return nil
}
