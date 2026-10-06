package docsync

import (
	"sort"
	"strings"
	"sync"
)

// DiagnosticRecord 是查询返回的有效诊断（携带登记次序）。
type DiagnosticRecord struct {
	Diagnostic
	Sequence int `json:"sequence"`
}

// DeadDiagnostic 是已失效清单条目。
type DeadDiagnostic struct {
	Diagnostic
	Sequence      int `json:"sequence"`
	BornVersion   int `json:"bornVersion"`
	FailedVersion int `json:"failedVersion"`
}

// Snapshot 是同一版本下的一致查询结果。
type Snapshot struct {
	Version     int                `json:"version"`
	Text        string             `json:"text"`
	Diagnostics []DiagnosticRecord `json:"diagnostics"`
	Dead        []DeadDiagnostic   `json:"dead"`
}

// ChangeResult 是一次变更的结果。
type ChangeResult struct {
	Accepted      bool
	BeforeVersion int
	AfterVersion  int
	Reason        error
	Dead          []int
}

const intMax = int(^uint(0) >> 1)

// Service 是线程安全的文档同步与诊断迁移服务。
type Service struct {
	mu      sync.RWMutex
	version int
	text    *ropeNode
	totalCU int
	byStart *dNode
	byEnd   *dNode
	nextSeq int
	alive   map[int]diagPayload
	dead    []deadRecord
}

type diagPayload struct {
	severity Severity
	message  string
	bornVer  int
}

// NewService 用初始文本创建服务，版本号为 0。
func NewService(initial string) *Service {
	return &Service{
		text:    buildLeaves(initial),
		totalCU: len16(initial),
		alive:   make(map[int]diagPayload),
	}
}

// Version 返回当前版本号。
func (s *Service) Version() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}

// Text 返回当前完整文本。
func (s *Service) Text() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var sb strings.Builder
	ropeString(s.text, &sb)
	return sb.String()
}

type plannedEdit struct {
	s, e int
	text string
	tl   int
	orig int
}

// Change 应用一组同时坐标编辑；任何校验失败都整体拒绝。
func (s *Service) Change(baseVersion int, edits []Edit) ChangeResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	res := ChangeResult{BeforeVersion: s.version, AfterVersion: s.version}
	if baseVersion != s.version {
		res.Reason = ErrStaleVersion
		return res
	}

	planned := make([]plannedEdit, 0, len(edits))
	for i, ed := range edits {
		st, err := positionToOffset(s.text, s.totalCU, ed.Range.Start)
		if err != nil {
			res.Reason = err
			return res
		}
		en, err := positionToOffset(s.text, s.totalCU, ed.Range.End)
		if err != nil {
			res.Reason = err
			return res
		}
		if st > en {
			res.Reason = ErrInvalidRange
			return res
		}
		planned = append(planned, plannedEdit{s: st, e: en, text: ed.Text, tl: len16(ed.Text), orig: i})
	}

	order := make([]plannedEdit, len(planned))
	copy(order, planned)
	sort.SliceStable(order, func(i, j int) bool {
		if order[i].s != order[j].s {
			return order[i].s < order[j].s
		}
		ie, je := order[i].e == order[i].s, order[j].e == order[j].s
		if ie != je {
			return ie
		}
		return order[i].orig < order[j].orig
	})
	for i := 1; i < len(order); i++ {
		prev, cur := order[i-1], order[i]
		if prev.e > prev.s && prev.e > cur.s {
			res.Reason = ErrOverlappingEdits
			return res
		}
	}

	afterVersion := s.version + 1
	dead := s.migrateDiagnostics(order, afterVersion)

	root := s.text
	// 编辑范围相对同一旧文档且互不重叠，在 rope 上从右向左应用即可
	// 保持每个范围的旧坐标不被先前的替换平移。
	for i := len(order) - 1; i >= 0; i-- {
		ed := order[i]
		root = replaceRope(root, ed.s, ed.e, ed.text)
	}
	s.text = root
	s.totalCU = computeTotalCU(order, s.totalCU)
	s.version = afterVersion

	res.Accepted = true
	res.AfterVersion = afterVersion
	res.Dead = dead
	return res
}

func computeTotalCU(edits []plannedEdit, old int) int {
	d := 0
	for _, ed := range edits {
		d += ed.tl - (ed.e - ed.s)
	}
	return old + d
}

// rngFromOffsets 用变更前 rope 把偏移还原为 Range（记录失效诊断时使用）。
func rngFromOffsets(root *ropeNode, totalCU, startOff, endOff int) Range {
	return Range{
		Start: offsetToPosition(root, totalCU, startOff),
		End:   offsetToPosition(root, totalCU, endOff),
	}
}

// migrateDiagnostics 在旧坐标上完成失效与迁移，返回新失效诊断登记号。
// 原子操作 = 非空替换，或同点连续插入合并后的插入组。按逆序施加懒平移，
// 逆序保证每个原子的旧坐标阈值仍能正确切分当前树（右侧操作的净平移不会
// 使节点跨过左侧原子的边界点，证明见 DESIGN 4.1/4.3）。
func (s *Service) migrateDiagnostics(edits []plannedEdit, afterVersion int) []int {
	var hits []deadHit
	for _, ed := range edits {
		if ed.e > ed.s {
			hits = collectDead(s.byEnd, ed.s, ed.e, hits)
		}
	}

	deadSeq := make([]int, 0, len(hits))
	deadBySeq := make(map[int]deadHit, len(hits))
	seen := make(map[int]struct{}, len(hits))
	for _, h := range hits {
		if _, dup := seen[h.sequence]; dup {
			continue
		}
		seen[h.sequence] = struct{}{}
		deadSeq = append(deadSeq, h.sequence)
		deadBySeq[h.sequence] = h
	}
	for _, seq := range deadSeq {
		h := deadBySeq[seq]
		p := s.alive[seq]
		rec := deadDiagnostic{
			diag: Diagnostic{
				Range:    rngFromOffsets(s.text, s.totalCU, h.start, h.end),
				Severity: p.severity,
				Message:  p.message,
			},
			sequence:      seq,
			bornVersion:   p.bornVer,
			failedVersion: afterVersion,
			start:         h.start,
			end:           h.end,
		}
		s.dead = append(s.dead, deadRecord{rec: rec, failedVersion: afterVersion})
		s.byStart = dDeleteSeq(s.byStart, h.start, seq)
		s.byEnd = dDeleteSeq(s.byEnd, h.end, seq)
	}
	if len(deadSeq) > 0 {
		alive := make(map[int]diagPayload, len(s.alive)-len(deadSeq))
		for seq, p := range s.alive {
			if _, gone := seen[seq]; !gone {
				alive[seq] = p
			}
		}
		s.alive = alive
	}

	// 构造原子操作序列（与 edits 同序）：同点连续插入合并。
	type atom struct {
		s, e int
		tl   int
	}
	var atoms []atom
	for _, ed := range edits {
		if n := len(atoms); n > 0 && ed.e == ed.s && atoms[n-1].e == atoms[n-1].s && atoms[n-1].s == ed.s {
			atoms[n-1].tl += ed.tl
			continue
		}
		atoms = append(atoms, atom{s: ed.s, e: ed.e, tl: ed.tl})
	}
	// 逆序施加、全部使用变更前旧坐标阈值。右侧编辑先作用后，只会平移键
	// 更大（位于其编辑点右侧）的节点，因此左侧编辑的旧阈值仍能正确切分
	// 当前树；多个相接/相邻编辑因而等价于端点各自独立映射后合并。
	for i := len(atoms) - 1; i >= 0; i-- {
		a := atoms[i]
		s2 := a.s
		if a.e > a.s {
			e2 := a.e
			d := a.tl - (a.e - a.s)
			// 例外：start==s2 且 end>=e2 的边界存活诊断（start 不随替换移动，
			// 但其 end 在 e2 之后需平移），无法与常规子树同量平移，先摘除后重插。
			var bounds []except
			bounds = enumerateRepBoundary(s.byEnd, s2, e2, bounds)
			s.removeExceptions(bounds)
			// 常规节点：start>s2（存活即 >=e2）与 end>=e2 分别整段平移。
			s.byStart = addDeltaRange(s.byStart, s2+1, intMax, d)
			s.byEnd = addDeltaRange(s.byEnd, e2, intMax, d)
			s.reinsertExceptions(bounds, func(ex except) (int, int) {
				return s2, ex.end + d
			})
		} else {
			tl := a.tl
			// 例外：跨点诊断 start<s2<end（start 不动，end 后移），
			// 以及空诊断 start==end==s2（整体后移）。
			var excs []except
			excs = enumerateInsertExceptions(s.byEnd, s2, excs)
			s.removeExceptions(excs)
			// 常规节点：byStart 的 start>=s2 整段；byEnd 的 end>s2 整段。
			s.byStart = addDeltaRange(s.byStart, s2, intMax, tl)
			s.byEnd = addDeltaRange(s.byEnd, s2+1, intMax, tl)
			s.reinsertExceptions(excs, func(ex except) (int, int) {
				if ex.kind == 1 {
					return ex.start, ex.end + tl // 跨点：start 不变
				}
				return s2 + tl, s2 + tl // 空诊断整体后移
			})
		}
	}
	return deadSeq
}

// removeExceptions 从两棵树摘除例外诊断（按传入的当前坐标）。
func (s *Service) removeExceptions(excs []except) {
	for _, ex := range excs {
		s.byStart = dDeleteSeq(s.byStart, ex.start, ex.sequence)
		s.byEnd = dDeleteSeq(s.byEnd, ex.end, ex.sequence)
	}
}

// reinsertExceptions 用 fn 给出的新 (start,end) 把例外诊断插回两棵树。
func (s *Service) reinsertExceptions(excs []except, fn func(ex except) (int, int)) {
	for _, ex := range excs {
		ns, ne := fn(ex)
		p := s.alive[ex.sequence]
		entry := dEntry{
			key: ns, start: ns, end: ne, sequence: ex.sequence,
			severity: p.severity, message: p.message,
		}
		s.byStart = dInsertRole(s.byStart, entry, 0)
		entry.key = ne
		s.byEnd = dInsertRole(s.byEnd, entry, 1)
	}
}

// Register 登记一条诊断。拒绝优先级：过期 > 起点大于终点 > 越界 > 代理对中间
// 的统一顺序按题意为“过期最优先，其后起点大于终点、越界、代理对中间”。
func (s *Service) Register(baseVersion int, d Diagnostic) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if baseVersion != s.version {
		return 0, ErrStaleVersion
	}
	st, en, err := s.resolveRangeLocked(d.Range)
	if err != nil {
		return 0, err
	}
	s.nextSeq++
	seq := s.nextSeq
	s.alive[seq] = diagPayload{severity: d.Severity, message: d.Message, bornVer: s.version}
	s.byStart = dInsertRole(s.byStart, dEntry{
		key: st, start: st, end: en, sequence: seq,
		severity: d.Severity, message: d.Message,
	}, 0)
	s.byEnd = dInsertRole(s.byEnd, dEntry{
		key: en, start: st, end: en, sequence: seq,
		severity: d.Severity, message: d.Message,
	}, 1)
	return seq, nil
}

// resolveRangeLocked 解析范围：先判 start>end（两端位置已先各自校验：
// 越界先于代理对中间），再返回两端偏移。
func (s *Service) resolveRangeLocked(r Range) (int, int, error) {
	// start>end 优先于两端自身的位置合法性（题目：过期 > start>end > 越界 > 代理对中间）。
	if positionLess(r.End, r.Start) {
		return 0, 0, ErrInvalidRange
	}
	st, errS := positionToOffset(s.text, s.totalCU, r.Start)
	if errS != nil {
		return 0, 0, errS
	}
	en, errE := positionToOffset(s.text, s.totalCU, r.End)
	if errE != nil {
		return 0, 0, errE
	}
	return st, en, nil
}

// positionLess 按（行，列）字典序比较位置（无需校验合法性）。
func positionLess(a, b Position) bool {
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.Character < b.Character
}

// Snapshot 返回同一版本的文本、版本、有效诊断与失效清单。
func (s *Service) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var sb strings.Builder
	ropeString(s.text, &sb)

	entries := dInorder(s.byStart, nil)
	// 懒平移可能让不同旧键节点汇聚到同一新键，树的物理次序不再保证
	// (start,end,seq) 升序，因此显式排序（需求规定的查询次序）。
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].start != entries[j].start {
			return entries[i].start < entries[j].start
		}
		if entries[i].end != entries[j].end {
			return entries[i].end < entries[j].end
		}
		return entries[i].sequence < entries[j].sequence
	})
	diags := make([]DiagnosticRecord, 0, len(entries))
	for _, e := range entries {
		r := rngFromOffsets(s.text, s.totalCU, e.start, e.end)
		diags = append(diags, DiagnosticRecord{
			Diagnostic: Diagnostic{Range: r, Severity: e.severity, Message: e.message},
			Sequence:   e.sequence,
		})
	}
	dead := make([]DeadDiagnostic, 0, len(s.dead))
	for _, r := range deadList(s.dead).sorted() {
		dead = append(dead, DeadDiagnostic{
			Diagnostic:    r.diag,
			Sequence:      r.sequence,
			BornVersion:   r.bornVersion,
			FailedVersion: r.failedVersion,
		})
	}
	return Snapshot{
		Version:     s.version,
		Text:        sb.String(),
		Diagnostics: diags,
		Dead:        dead,
	}
}

// PositionToOffset 位置 → UTF-16 码元偏移；区分越界与代理对中间。
func (s *Service) PositionToOffset(p Position) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return positionToOffset(s.text, s.totalCU, p)
}

// OffsetToPosition UTF-16 码元偏移 → 位置。off 越界返回 ErrOutOfBounds；
// off 落在代理对中间返回 ErrInsideSurrogatePair。
func (s *Service) OffsetToPosition(off int) (Position, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if off < 0 || off > s.totalCU {
		return Position{}, ErrOutOfBounds
	}
	if !s.isRuneBoundaryOffset(off) {
		return Position{}, ErrInsideSurrogatePair
	}
	return offsetToPosition(s.text, s.totalCU, off), nil
}

// isRuneBoundaryOffset 判断文档级偏移是否为 rune 边界：沿树下降到叶块，
// 用叶块缓存的 rune 边界码元序列判定。
func (s *Service) isRuneBoundaryOffset(off int) bool {
	if off == s.totalCU {
		return true
	}
	leaf, rel := descendToLeaf(s.text, off)
	for _, b := range leaf.leafCU {
		if b == rel {
			return true
		}
	}
	return false
}
