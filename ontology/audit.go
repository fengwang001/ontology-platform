package ontology

import (
	"fmt"
	"sort"
)

// AuditRequest 历史一致性审计请求。
type AuditRequest struct {
	LinkType   LinkTypeID `json:"linkType"`
	RecordFrom int64      `json:"recordFrom"` // 记录时刻区间起点（含）
	RecordTo   int64      `json:"recordTo"`   // 记录时刻区间终点（含）
	ValidAt    int64      `json:"validAt"`    // 考察的有效时刻
	// ExpectedVersion 是请求方在发起审计时观测到的最新基数约束版本号。
	// 若请求被处理时最新版本已前进（期间发生了版本调整），
	// 则审计所依据的版本信息已作废，报告 ErrorVersionSuperseded。
	ExpectedVersion int `json:"expectedVersion"`
}

// ViolationRange 某对象在记录时刻区间 [From, To) 内持续违反基数的记录。
type ViolationRange struct {
	Object ObjectID    `json:"object"`
	From   int64       `json:"from"`
	To     int64       `json:"to"`
	Degree int         `json:"degree"`
	Limit  Cardinality `json:"limit"`
}

// SegmentResult 按基数约束版本切分的一段审计结论。
// 审计区间跨越版本调整时，结果按版本分段给出，绝不合并为单一结论。
type SegmentResult struct {
	From       int64            `json:"from"`
	To         int64            `json:"to"`
	Version    int              `json:"version"`
	Violations []ViolationRange `json:"violations"`
}

// AuditReport 审计输出。注意：本结构（及其嵌套类型）不包含任何
// 记录来源字段，审计输出无法反推事实最初由哪一端记录。
type AuditReport struct {
	Request  AuditRequest    `json:"request"`
	Segments []SegmentResult `json:"segments,omitempty"`
	Err      *AuditError     `json:"error,omitempty"`
}

// AuditRecord 判定日志条目：每次审计判定的输入、所依据的约束版本
// 与对照结论均被追加记录，供事后核查。判定日志不属于链接历史轨迹。
type AuditRecord struct {
	Seq        int64           `json:"seq"`
	Request    AuditRequest    `json:"request"`
	Segments   []SegmentResult `json:"segments,omitempty"`
	ErrorClass ErrorClass      `json:"errorClass"`
	Detail     string          `json:"detail,omitempty"`
}

// versionAt 返回记录时刻 t 生效的基数约束版本（二分查找，O(log 版本数)）。
func versionAt(versions []ConstraintVersion, t int64) ConstraintVersion {
	i := sort.Search(len(versions), func(i int) bool {
		return versions[i].EffectiveFrom > t
	}) - 1
	if i < 0 {
		return versions[0]
	}
	return versions[i]
}

// Audit 对链接类型在记录时刻区间 [RecordFrom, RecordTo] 内的历史
// 进行基数一致性审计。审计为纯只读操作：无论结论如何（包括各类错误），
// 都不会对链接历史轨迹产生任何可观察的改动。
//
// 错误优先级（同时满足多类条件时只报告最高优先级一类）：
//  1. 区间自相矛盾；2. 对象类型尚不存在；3. 约束版本已作废；4. 镜像结构性缺失。
func (s *Store) Audit(req AuditRequest) (AuditReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	st, ok := s.links[req.LinkType]
	if !ok {
		return AuditReport{}, fmt.Errorf("link type %q not registered", req.LinkType)
	}
	report := AuditReport{Request: req}
	fail := func(class ErrorClass, detail string) AuditReport {
		report.Err = &AuditError{Class: class, Detail: detail}
		s.appendAuditRecord(req, nil, class, detail)
		return report
	}

	// 优先级 1：请求的记录时刻区间自相矛盾。
	if req.RecordFrom > req.RecordTo {
		return fail(ErrorContradictoryInterval,
			fmt.Sprintf("recordFrom %d > recordTo %d", req.RecordFrom, req.RecordTo)), nil
	}
	// 优先级 2：任一端对象类型在请求的记录时刻（区间起点）尚不存在。
	for _, ot := range []ObjectTypeID{st.def.LeftType, st.def.RightType} {
		if createdAt, ok := s.objects[ot]; !ok || createdAt > req.RecordFrom {
			return fail(ErrorObjectTypeMissing,
				fmt.Sprintf("object type %q does not exist at record time %d", ot, req.RecordFrom)), nil
		}
	}
	// 优先级 3：审计所依据的约束版本信息在请求处理期间已被新版本作废。
	if latest := st.versions[len(st.versions)-1].Version; req.ExpectedVersion != latest {
		return fail(ErrorVersionSuperseded,
			fmt.Sprintf("expected version %d, but latest is %d", req.ExpectedVersion, latest)), nil
	}

	// 按基数约束版本边界切分审计区间。
	bounds := []int64{req.RecordFrom}
	for _, v := range st.versions {
		if v.EffectiveFrom > req.RecordFrom && v.EffectiveFrom <= req.RecordTo {
			bounds = append(bounds, v.EffectiveFrom)
		}
	}
	bounds = append(bounds, req.RecordTo+1) // 末尾哨兵：段为 [from, next)

	mirrorDefect := false
	var segments []SegmentResult
	for i := 0; i+1 < len(bounds); i++ {
		segFrom, segTo := bounds[i], bounds[i+1]-1
		ver := versionAt(st.versions, segFrom)
		seg := s.auditSegment(st, req.ValidAt, segFrom, segTo, ver, &mirrorDefect)
		segments = append(segments, seg)
	}

	// 优先级 4：回放发现镜像一致性结构性缺失。
	if mirrorDefect {
		return fail(ErrorMirrorInconsistency,
			"symmetric link with single-endpoint record lacks corroboration"), nil
	}

	report.Segments = segments
	s.appendAuditRecord(req, segments, ErrorNone, "")
	return report, nil
}

// appendAuditRecord 追加判定日志（调用方须持锁）。
func (s *Store) appendAuditRecord(req AuditRequest, segs []SegmentResult, class ErrorClass, detail string) {
	s.auditLog = append(s.auditLog, AuditRecord{
		Seq:        int64(len(s.auditLog) + 1),
		Request:    req,
		Segments:   segs,
		ErrorClass: class,
		Detail:     detail,
	})
}

// auditSegment 评估单个版本段 [segFrom, segTo] 内的基数违反情况。
// 从 segFrom 时刻的状态出发，沿段内提交增量推进，仅跟踪受影响对象的
// 度数变化，代价与段内提交数成正比，与全部历史无关。
func (s *Store) auditSegment(st *linkTypeState, vt, segFrom, segTo int64, ver ConstraintVersion, mirrorDefect *bool) SegmentResult {
	states := s.replayState(st, segFrom)

	alive := make(map[pair]bool, len(states))
	degree := make(map[ObjectID]int)
	for p, ps := range states {
		present := false
		for _, iv := range ps.ivs {
			if iv.covers(vt) {
				present = true
				break
			}
		}
		if present {
			alive[p] = true
			degree[p.L]++
			degree[p.R]++
			if ps.deficit {
				*mirrorDefect = true
			}
		}
	}

	// limitOf 返回对象作为某一对端时适用的基数约束。
	// 对称链接两侧共用 Left 约束（见设计文档）。
	limitOf := func(isLeft bool) Cardinality {
		if isLeft || st.def.Symmetric {
			return ver.Left
		}
		return ver.Right
	}

	type openViolation struct {
		from   int64
		degree int
		limit  Cardinality
	}
	open := make(map[ObjectID]openViolation)
	var violations []ViolationRange
	// side 记录对象当前作为左/右端出现的存活链接数（引用计数），
	// 链接撤销时递减，避免把已消亡的侧向身份残留到后续判定。
	side := make(map[ObjectID][2]int)

	closeAt := func(obj ObjectID, at int64) {
		if ov, ok := open[obj]; ok {
			violations = append(violations, ViolationRange{
				Object: obj, From: ov.from, To: at,
				Degree: ov.degree, Limit: ov.limit,
			})
			delete(open, obj)
		}
	}
	// recheck 在 at 时刻重新评估 obj 的违反状态。
	// 对象实际出现在哪两侧由 side 表决定，两侧约束分别检查。
	recheck := func(obj ObjectID, at int64) {
		sd := side[obj]
		d := degree[obj]
		violating := false
		var limit Cardinality
		if sd[0] > 0 || st.def.Symmetric {
			if l := limitOf(true); l.violated(d) {
				violating, limit = true, l
			}
		}
		if !violating && sd[1] > 0 && !st.def.Symmetric {
			if l := limitOf(false); l.violated(d) {
				violating, limit = true, l
			}
		}
		ov, wasOpen := open[obj]
		switch {
		case violating && (!wasOpen || ov.degree != d || ov.limit != limit):
			closeAt(obj, at)
			open[obj] = openViolation{from: at, degree: d, limit: limit}
		case !violating && wasOpen:
			closeAt(obj, at)
		}
	}

	// 段起点的全量评估。
	for p := range alive {
		sd := side[p.L]
		sd[0]++
		side[p.L] = sd
		sd = side[p.R]
		sd[1]++
		side[p.R] = sd
	}
	for obj := range side {
		recheck(obj, segFrom)
	}

	// 沿段内提交增量推进。二分定位段内首条事实，
	// 代价与段之前的累计历史无关。
	start := sort.Search(len(st.facts), func(i int) bool {
		return st.facts[i].RecordedAt > segFrom
	})
	for i := start; i < len(st.facts); i++ {
		f := st.facts[i]
		if f.RecordedAt > segTo {
			break
		}
		p := pair{L: f.Left, R: f.Right}
		wasAlive := alive[p]
		applyFact(states, f, st.def)
		ps := states[p]
		nowAlive := false
		for _, iv := range ps.ivs {
			if iv.covers(vt) {
				nowAlive = true
				break
			}
		}
		if nowAlive && ps.deficit {
			*mirrorDefect = true
		}
		if wasAlive == nowAlive {
			continue
		}
		delta := 1
		if !nowAlive {
			delta = -1
		}
		degree[p.L] += delta
		degree[p.R] += delta
		alive[p] = nowAlive
		sdL := side[p.L]
		sdL[0] += delta
		side[p.L] = sdL
		sdR := side[p.R]
		sdR[1] += delta
		side[p.R] = sdR
		recheck(p.L, f.RecordedAt)
		recheck(p.R, f.RecordedAt)
	}

	// 段结束：闭合所有未闭合的违反区间（半开区间，终点为 segTo+1）。
	for obj := range open {
		closeAt(obj, segTo+1)
	}
	sort.Slice(violations, func(i, j int) bool {
		if violations[i].From != violations[j].From {
			return violations[i].From < violations[j].From
		}
		return violations[i].Object < violations[j].Object
	})
	return SegmentResult{
		From:       segFrom,
		To:         segTo,
		Version:    ver.Version,
		Violations: violations,
	}
}
