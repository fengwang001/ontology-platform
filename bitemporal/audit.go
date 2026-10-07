package bitemporal

import "context"

// AuditRequest 描述一次历史一致性审计。
type AuditRequest struct {
	LinkType    ID
	RecordStart int64 // 含
	RecordEnd   int64 // 不含
	// ValidTime 为审计采用的有效时间；为 0 时使用“记录时刻即有效时刻”的对角线回放。
	ValidTime int64
}

// boundary 是审计窗口内某对象违反状态的一次跳变。
type boundary struct {
	at    int64
	dir   string
	obj   ID
	enter bool
}

// Auditor 在 Store 之上提供分段历史审计与判定留痕。
type Auditor struct {
	store *Store
	log   *DecisionLog

	// BasisHook 仅用于测试：在固定快照与最终基版校验之间被调用，
	// 可在其中并发调整基数约束版本以触发 E1。
	BasisHook func(snapGen int64)
}

// NewAuditor 创建审计器。
func NewAuditor(st *Store, log *DecisionLog) *Auditor {
	return &Auditor{store: st, log: log}
}

// Audit 执行审计。错误优先级固定为 E3 > E2 > E1 > E4；
// 任何错误都只产生审计输出，绝不修改链接历史。
func (a *Auditor) Audit(ctx context.Context, req AuditRequest) ([]Segment, error) {
	rec := DecisionRecord{Request: req}
	snap := a.store.CurrentSnapshot()
	rec.SnapshotGen = snap.gen

	fail := func(k error, detail string) ([]Segment, error) {
		rec.Err = k.Error()
		a.log.append(rec)
		return nil, &AuditError{Kind: k, Detail: detail}
	}

	// E3：区间自相矛盾。
	if req.RecordEnd <= req.RecordStart {
		return fail(ErrIntervalContradiction, "record interval end must be greater than start")
	}

	tk, ok := snap.types[req.LinkType]
	// E2：链接类型 / 首版规则 / 端点对象类型在请求记录时刻尚不存在。
	if !ok {
		return fail(ErrObjectTypeMissing, "link type is not registered")
	}
	if _, ruleOK := snap.RuleAt(req.LinkType, req.RecordStart); !ruleOK {
		return fail(ErrObjectTypeMissing, "no cardinality rule in effect at the requested record time")
	}
	if rule, ruleOK := snap.RuleAt(req.LinkType, req.RecordStart); ruleOK {
		rec.RuleBasis = rule.FromRecord
	}
	if !snap.ObjectTypeExists(tk.lt.SourceType, req.RecordStart) ||
		!snap.ObjectTypeExists(tk.lt.TargetType, req.RecordStart) {
		return fail(ErrObjectTypeMissing, "an endpoint object type does not exist at the requested record time")
	}

	idx := snap.index[req.LinkType]
	if idx == nil {
		return fail(ErrObjectTypeMissing, "link type has no usable index at the requested record time")
	}

	// E4 内容计算（结构性镜像缺失）。
	structural := false
	var firstDefect MirrorDefect
	if req.ValidTime == 0 {
		if hits := idx.defects.overlapWindow(req.RecordStart, req.RecordEnd); len(hits) > 0 {
			structural = true
			firstDefect = decodeDefect(snap, req.LinkType, hits[0])
		}
	} else {
		r := snap.replay(req.LinkType, req.RecordStart, req.ValidTime, nil)
		if len(r.Defects) > 0 {
			structural = true
			firstDefect = r.Defects[0]
		}
	}

	// E1：提交前确认所依据的规则版本未在处理期间被作废。
	basis := snap.ruleGen
	if a.BasisHook != nil {
		a.BasisHook(snap.gen)
	}
	if a.store.CurrentSnapshot().ruleGen != basis {
		return fail(ErrRuleVersionSuperseded, "rule basis superseded during audit")
	}

	if structural {
		return fail(ErrMirrorStructural,
			"structural mirror inconsistency: pair "+
				string(firstDefect.A)+" <-> "+string(firstDefect.B)+
				" missing side "+firstDefect.MissingSide)
	}

	segs := buildSegments(snap, tk, idx, req)
	rec.Segments = segs
	a.log.append(rec)
	return segs, nil
}

func decodeDefect(snap *Snapshot, linkType ID, hit ivl) MirrorDefect {
	idx := snap.index[linkType]
	hc := idx.codes[hit.val]
	missing := hc.side
	pairCode := hit.val
	if missing == halfBA || missing == halfR {
		pairCode = hit.val ^ 1
	}
	pc := idx.codes[pairCode]
	return MirrorDefect{
		LinkType:    linkType,
		A:           pc.a,
		B:           pc.b,
		MissingSide: missing,
		RecordTime:  hit.lo,
	}
}
