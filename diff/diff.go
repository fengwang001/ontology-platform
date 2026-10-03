// Package diff 维护源/目标两侧表，按主键归并比较归一化后的行，
// 并提供版本计数与供 plan 包使用的原子事务原语。
package diff

import (
	"errors"
	"sort"
	"sync"
	"sync/atomic"

	"ontology/norm"
)

// ErrParam 为参数非法（与 norm.ErrParam 同一错误）。
var ErrParam = norm.ErrParam

// ErrStale 表示计划中至少一项的当前目标状态与记录版本不符。
var ErrStale = errors.New("diff: stale plan")

// MaxID 为合法主键上界；Compare 的 hi 可取 MaxID+1。
const MaxID int64 = 1_000_000_000

// 行分类。
const (
	Equal   = 0 // 两侧都在且归一值全相等
	Changed = 1 // 两侧都在但至少一列不等
	Missing = 2 // 仅源侧存在
	Extra   = 3 // 仅目标侧存在
)

// 列序号：d=0，c=1。
const (
	ColD = 0
	ColC = 1
)

// Row 为一行：id、可空十进制尾数 d（nil 表示 NULL）、可空字节串 c（nil 表示 NULL）。
type Row struct {
	ID int64
	D  *int64
	C  []byte
}

// Result 为 Compare 的一行结果。SD/SC 为源值归一结果，TD/TC 为目标比较所用值。
type Result struct {
	ID    int64
	Kind  int
	Cols  []int  // Changed 时不等的列，按 d、c 次序
	SD    *int64 // 源 d 归一值
	SC    []byte // 源 c 归一值
	TD    *int64 // 目标 d（原样）
	TC    []byte // 目标 c 归一值
	Ver   int64  // 结果生成时目标行版本（不存在时为 0）
	Exist bool   // 目标行是否存在
}

// Mutation 为对单个目标行的条件写。
type Mutation struct {
	ID    int64
	Kind  int    // 0=Insert, 1=Update, 2=Delete
	Ver   int64  // 期望版本（与 Exist 共同构成前置条件）
	Exist bool   // 期望目标行是否存在
	D     *int64 // Insert/Update 的 d（归一值，nil=NULL）
	C     []byte // Insert/Update 的 c（归一值）
	Cols  []int  // Update 时要写的列；Insert 视为全行
}

type entry struct {
	d     *int64 // 行存在但 d 为 NULL 时 d 仍为 nil；用 alive 区分墓碑
	c     []byte
	ver   int64
	alive bool
}

// Engine 为并发安全的两侧表。
type Engine struct {
	mu      sync.RWMutex
	n       *norm.N
	src     map[int64]*entry
	tgt     map[int64]*entry
	lastVis atomic.Int64 // 最近一次 Compare 访问的非导出行数（范围内两侧行数之和）
}

// New 创建对账引擎；参数非法时返回错误。
func New(sd, rm int, ne bool) (*Engine, error) {
	n, err := norm.New(sd, rm, ne)
	if err != nil {
		return nil, err
	}
	return &Engine{n: n, src: map[int64]*entry{}, tgt: map[int64]*entry{}}, nil
}

// SrcPut 写入/覆盖源行（不影响任何版本）。
func (e *Engine) SrcPut(r Row) error {
	if err := norm.CheckRow(r.ID, r.D, r.C); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.src[r.ID] = &entry{d: cloneD(r.D), c: cloneC(r.C), alive: true}
	return nil
}

// TgtPut 写入/覆盖目标行，该 id 目标版本加 1（删除后再写也继续累加）。
func (e *Engine) TgtPut(r Row) error {
	if err := norm.CheckRow(r.ID, r.D, r.C); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.tgt[r.ID] = &entry{d: cloneD(r.D), c: cloneC(r.C), ver: verOf(e.tgt[r.ID]) + 1, alive: true}
	return nil
}

// TgtDel 删除目标行，该 id 目标版本加 1；行不存在也累加版本。
func (e *Engine) TgtDel(id int64) error {
	if id < 1 || id > MaxID {
		return ErrParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.tgt[id] = &entry{ver: verOf(e.tgt[id]) + 1} // 墓碑：版本保留，行不存在
	return nil
}

// Compare 归并比较 [lo, hi) 内两侧行，结果按 id 升序。只读。
func (e *Engine) Compare(lo, hi int64) ([]Result, error) {
	if lo < 1 || hi < lo || hi > MaxID+1 {
		return nil, ErrParam
	}
	e.mu.RLock()
	defer e.mu.RUnlock()

	ids := unionIDs(e.src, e.tgt, lo, hi)
	out := make([]Result, 0, len(ids))
	visit := 0
	for _, id := range ids {
		s, sok := e.src[id]
		t, tok := e.tgt[id]
		tExist := tok && t.alive
		if sok {
			visit++
		}
		if tExist {
			visit++
		}
		res := Result{ID: id, Exist: tExist}
		switch {
		case sok && !tExist:
			res.Kind = Missing
			res.SD = e.n.CvtD(s.d)
			res.SC = e.n.NormC(s.c)
		case !sok && tExist:
			res.Kind = Extra
			res.TD = cloneD(t.d)
			res.TC = e.n.NormC(t.c)
			res.Ver = t.ver
		default:
			sd := e.n.CvtD(s.d)
			sc := e.n.NormC(s.c)
			tc := e.n.NormC(t.c)
			res.SD, res.SC = sd, sc
			res.TD, res.TC = cloneD(t.d), tc
			res.Ver = t.ver
			if !dEqual(sd, t.d) {
				res.Kind = Changed
				res.Cols = append(res.Cols, ColD)
			}
			if !cEqual(sc, tc) {
				res.Kind = Changed
				res.Cols = append(res.Cols, ColC)
			}
		}
		out = append(out, res)
	}
	e.lastVis.Store(int64(visit))
	return out, nil
}

// TryMutate 在单个临界区内先整体校验所有前置条件，全部满足后整体应用；
// 任一失配返回 (最小失配 id, ErrStale) 且零改动。调用方须保证 muts 按 id 升序且无重复。
func (e *Engine) TryMutate(muts []Mutation) (int64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	for _, m := range muts {
		if m.ID < 1 || m.ID > MaxID {
			return m.ID, ErrParam
		}
		t, ok := e.tgt[m.ID]
		exist := ok && t.alive
		if exist != m.Exist || (exist && t.ver != m.Ver) {
			return m.ID, ErrStale
		}
	}
	for _, m := range muts {
		switch m.Kind {
		case 0: // Insert
			e.tgt[m.ID] = &entry{d: cloneD(m.D), c: cloneC(m.C), ver: 1, alive: true}
		case 1: // Update：仅替换 Cols 中的列
			cur := e.tgt[m.ID]
			nd, nc := cloneD(cur.d), cloneC(cur.c)
			for _, col := range m.Cols {
				switch col {
				case ColD:
					nd = cloneD(m.D)
				case ColC:
					nc = cloneC(m.C)
				}
			}
			cur.d, cur.c, cur.ver = nd, nc, cur.ver+1
		case 2: // Delete
			cur := e.tgt[m.ID]
			cur.d, cur.c, cur.alive = nil, nil, false
			cur.ver++
		}
	}
	return 0, nil
}

// TgtSnapshot 返回目标行是否存在、版本与归一值拷贝；不存在时 exist=false。
func (e *Engine) TgtSnapshot(id int64) (d *int64, c []byte, ver int64, exist bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	t, ok := e.tgt[id]
	if !ok || !t.alive {
		return nil, nil, verOf(t), false
	}
	return cloneD(t.d), e.n.NormC(t.c), t.ver, true
}

// SrcSnapshot 返回源行是否存在及原始值拷贝。
func (e *Engine) SrcSnapshot(id int64) (d *int64, c []byte, exist bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	s, ok := e.src[id]
	if !ok {
		return nil, nil, false
	}
	return cloneD(s.d), cloneC(s.c), true
}

// LastVisitCount 返回最近一次 Compare 访问的非导出行数。
func (e *Engine) LastVisitCount() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return int(e.lastVis.Load())
}

// Norm 回显归一化器。
func (e *Engine) Norm() *norm.N { return e.n }

func unionIDs(src, tgt map[int64]*entry, lo, hi int64) []int64 {
	set := make(map[int64]struct{})
	for id, en := range src {
		if id >= lo && id < hi && en.alive {
			set[id] = struct{}{}
		}
	}
	for id, en := range tgt {
		if id >= lo && id < hi && en.alive {
			set[id] = struct{}{}
		}
	}
	ids := make([]int64, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func cloneD(d *int64) *int64 {
	if d == nil {
		return nil
	}
	v := *d
	return &v
}

func cloneC(c []byte) []byte {
	if c == nil {
		return nil
	}
	out := make([]byte, len(c))
	copy(out, c)
	return out
}

func dEqual(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func cEqual(a, b []byte) bool {
	// nil（NULL）与非 nil 空串（空串）不等；ne=true 时两者已在归一阶段统一为 nil。
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return string(a) == string(b)
}

func verOf(t *entry) int64 {
	if t == nil {
		return 0
	}
	return t.ver
}
