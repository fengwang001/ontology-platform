package audit

import (
	"time"
)

// System 是权限决策审计回放系统。所有公开方法均可并发调用，
// 任意并发调用的结果等价于某个串行顺序下逐条处理的结果（可线性化）。
type System struct {
	st     *store
	logger Logger
	now    func() time.Time
}

// Option 配置 System。
type Option func(*System)

// WithLogger 设置调用日志记录器。
func WithLogger(l Logger) Option {
	return func(s *System) { s.logger = l }
}

// WithClock 设置时钟（测试可注入固定时钟）。
func WithClock(now func() time.Time) Option {
	return func(s *System) { s.now = now }
}

// New 创建一个空的审计回放系统。
func New(opts ...Option) *System {
	s := &System{st: newStore(), now: time.Now}
	for _, o := range opts {
		o(s)
	}
	return s
}

// SubmitRuleVersion 提交一次权限规则变更，生成新版本并完整保留旧版本。
// 返回新版本的标识（版本顺序由系统分配的单调序号唯一确定）。
func (s *System) SubmitRuleVersion(content RuleSet) (VersionID, error) {
	s.st.mu.Lock()
	v := s.st.appendVersion(content)
	s.st.mu.Unlock()

	s.log(Event{
		Op:     "submit_version",
		Input:  content,
		Output: v.ID,
		Basis:  map[string]any{"seq": v.Seq, "content_hash": v.ContentHash, "chain_hash": v.ChainHash},
	})
	return v.ID, nil
}

// RecordAccess 记录一次实际发生的访问判定。
// result 由平台决策点（PDP）给出，审计模块如实记录；
// 回放时将以登记的版本标识重新独立裁决。
// 若登记的版本标识不存在，请求被拒绝且不产生任何记录。
func (s *System) RecordAccess(subject, target string, req Request, decidedAt time.Time, versionID VersionID, result Decision) (RecordID, error) {
	s.st.mu.Lock()
	v, ok := s.st.getVersion(versionID)
	if !ok {
		s.st.mu.Unlock()
		s.log(Event{Op: "record_access", Input: recordAccessInput{subject, target, req, decidedAt, versionID, result}, Err: ErrVersionNotFound.Error()})
		return "", ErrVersionNotFound
	}
	rec := &AuditRecord{
		Subject:     subject,
		Target:      target,
		Request:     req,
		DecidedAt:   decidedAt,
		Result:      result,
		VersionID:   versionID,
		VersionHash: v.ContentHash,
	}
	s.st.appendRecord(rec)
	id := rec.ID
	s.st.mu.Unlock()

	s.log(Event{
		Op:     "record_access",
		Input:  recordAccessInput{subject, target, req, decidedAt, versionID, result},
		Output: id,
		Basis:  map[string]any{"version_id": versionID, "version_hash": rec.VersionHash, "record_seq": rec.Seq},
	})
	return id, nil
}

type recordAccessInput struct {
	Subject   string    `json:"subject"`
	Target    string    `json:"target"`
	Request   Request   `json:"request"`
	DecidedAt time.Time `json:"decided_at"`
	VersionID VersionID `json:"version_id"`
	Result    Decision  `json:"result"`
}

// Replay 依据记录中登记的版本标识重新执行裁决并与原结果比较。
// 回放仅依据该版本规则内容与记录中登记的输入，不依赖任何缓存状态。
//
// 错误汇报优先级固定：版本标识不存在 > 审计记录完整性破坏。
// 版本规则内容被篡改不是错误而是回放结论（ReplayVersionTampered）。
func (s *System) Replay(recordID RecordID) (ReplayResult, ReadStats, error) {
	s.st.mu.Lock()
	defer s.st.mu.Unlock()

	var stats ReadStats
	rec, ok := s.st.getRecord(recordID)
	if !ok {
		s.log(Event{Op: "replay", Input: recordID, Err: ErrRecordNotFound.Error()})
		return ReplayResult{}, stats, ErrRecordNotFound
	}
	stats.RecordsScanned = 1

	// 优先级 1：登记的版本标识不存在。
	v, ok := s.st.getVersion(rec.VersionID)
	if !ok {
		s.log(Event{Op: "replay", Input: recordID, Err: ErrVersionNotFound.Error(),
			Basis: map[string]any{"missing_version": rec.VersionID}})
		return ReplayResult{}, stats, ErrVersionNotFound
	}
	stats.VersionsScanned = 1
	stats.VersionBytesRead = len(Canonical(v.Content))

	// 优先级 2：原始审计记录完整性破坏。
	if !verifyRecord(rec) {
		s.log(Event{Op: "replay", Input: recordID, Err: ErrAuditIntegrity.Error(),
			Basis: map[string]any{"record_id": rec.ID, "stored_self_hash": rec.SelfHash}})
		return ReplayResult{}, stats, ErrAuditIntegrity
	}

	res := ReplayResult{
		RecordID:    rec.ID,
		Original:    rec.Result,
		VersionID:   rec.VersionID,
		VersionHash: rec.VersionHash,
	}

	// 版本内容完整性：当前存储内容必须仍与判定时登记的哈希一致。
	if !verifyVersion(v) || v.ContentHash != rec.VersionHash {
		res.Outcome = ReplayVersionTampered
		res.Recomputed = rec.Result // 不可信内容不参与重算结论
		s.log(Event{Op: "replay", Input: recordID, Output: res,
			Basis: map[string]any{"version_id": v.ID, "expected_hash": rec.VersionHash, "stored_hash": v.ContentHash}})
		return res, stats, nil
	}

	// 纯函数重算：仅依据该版本规则内容与记录登记的输入。
	res.Recomputed = Evaluate(v.Content, rec.Subject, rec.Target, rec.Request)
	if res.Recomputed == rec.Result {
		res.Outcome = ReplayConsistent
	} else {
		res.Outcome = ReplayMisjudgment
	}
	s.log(Event{Op: "replay", Input: recordID, Output: res,
		Basis: map[string]any{"version_id": v.ID, "version_hash": v.ContentHash, "record_seq": rec.Seq}})
	return res, stats, nil
}

// AppendCorrection 追加一条纠正记录。原始记录不被修改或删除。
// supersedes 为空表示直接针对原始记录；否则必须指向该记录当前纠正链的链头。
// 目标原始记录不存在时请求被拒绝（ErrCorrectionTargetMissing），不产生任何影响。
func (s *System) AppendCorrection(recordID RecordID, supersedes CorrectionID, correctedTo Decision, reason string) (CorrectionID, error) {
	s.st.mu.Lock()
	defer s.st.mu.Unlock()

	input := map[string]any{"record_id": recordID, "supersedes": supersedes, "corrected_to": correctedTo, "reason": reason}

	if _, ok := s.st.getRecord(recordID); !ok {
		s.log(Event{Op: "append_correction", Input: input, Err: ErrCorrectionTargetMissing.Error()})
		return "", ErrCorrectionTargetMissing
	}

	chain := s.st.byRecord[recordID]
	if supersedes == "" {
		if len(chain) != 0 {
			s.log(Event{Op: "append_correction", Input: input, Err: ErrCorrectionChain.Error()})
			return "", ErrCorrectionChain
		}
	} else {
		sc, ok := s.st.getCorrection(supersedes)
		if !ok {
			s.log(Event{Op: "append_correction", Input: input, Err: ErrCorrectionNotFound.Error()})
			return "", ErrCorrectionNotFound
		}
		if sc.RecordID != recordID || len(chain) == 0 || chain[len(chain)-1] != supersedes {
			s.log(Event{Op: "append_correction", Input: input, Err: ErrCorrectionChain.Error()})
			return "", ErrCorrectionChain
		}
	}

	c := &Correction{
		RecordID:    recordID,
		Supersedes:  supersedes,
		CorrectedTo: correctedTo,
		Reason:      reason,
		RecordedAt:  s.now(),
	}
	s.st.appendCorrection(c)
	s.log(Event{Op: "append_correction", Input: input, Output: c.ID,
		Basis: map[string]any{"record_id": recordID, "correction_seq": c.Seq, "chain_len": len(chain) + 1}})
	return c.ID, nil
}

// QueryLegality 回答"某主体在某个历史时刻的访问是否合法"，返回三态结果。
// 结果仅是日志状态的纯函数，与查询发出的时刻无关。
// 返回该时刻的审计记录副本与其完整纠正链。
func (s *System) QueryLegality(subject, target string, at time.Time) (Legality, *AuditRecord, []Correction, error) {
	s.st.mu.Lock()
	defer s.st.mu.Unlock()

	input := map[string]any{"subject": subject, "target": target, "at": at}
	var found *AuditRecord
	for _, id := range s.st.recordOrder {
		r := s.st.records[id]
		if r.Subject == subject && r.Target == target && r.DecidedAt.Equal(at) {
			found = r // 同一时刻多条时取最新一条（seq 最大）
		}
	}
	if found == nil {
		s.log(Event{Op: "query_legality", Input: input, Err: ErrRecordNotFound.Error()})
		return LegalityDenyAsRecorded, nil, nil, ErrRecordNotFound
	}
	leg, _ := effectiveLocked(s.st, found)
	rec := cloneRecord(found)
	corrections := s.correctionsOfLocked(found.ID)
	s.log(Event{Op: "query_legality", Input: input, Output: leg,
		Basis: map[string]any{"record_id": found.ID, "corrections": correctionIDs(corrections)}})
	return leg, rec, corrections, nil
}

// EffectiveDecision 返回某条审计记录的当前有效结论（考虑纠正链）。
func (s *System) EffectiveDecision(recordID RecordID) (Legality, Decision, error) {
	s.st.mu.Lock()
	defer s.st.mu.Unlock()

	rec, ok := s.st.getRecord(recordID)
	if !ok {
		s.log(Event{Op: "effective_decision", Input: recordID, Err: ErrRecordNotFound.Error()})
		return LegalityDenyAsRecorded, DecisionDeny, ErrRecordNotFound
	}
	leg, eff := effectiveLocked(s.st, rec)
	s.log(Event{Op: "effective_decision", Input: recordID, Output: map[string]any{"legality": leg.String(), "effective": eff},
		Basis: map[string]any{"record_id": recordID, "corrections": s.st.byRecord[recordID]}})
	return leg, eff, nil
}

// effectiveLocked 计算有效结论：纠正链链头的结论为当前有效结论；
// 若有效结论与原始记录不同，则三态回答为"已被纠正"。
func effectiveLocked(st *store, rec *AuditRecord) (Legality, Decision) {
	eff := rec.Result
	if chain := st.byRecord[rec.ID]; len(chain) > 0 {
		eff = st.corrections[chain[len(chain)-1]].CorrectedTo
	}
	if eff != rec.Result {
		return LegalityCorrected, eff
	}
	if rec.Result == DecisionAllow {
		return LegalityAllowAsRecorded, eff
	}
	return LegalityDenyAsRecorded, eff
}

// GetRecord 返回审计记录的只读副本。
func (s *System) GetRecord(id RecordID) (AuditRecord, error) {
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	r, ok := s.st.getRecord(id)
	if !ok {
		return AuditRecord{}, ErrRecordNotFound
	}
	return *cloneRecord(r), nil
}

// GetVersion 返回规则版本的只读副本。
func (s *System) GetVersion(id VersionID) (RuleVersion, error) {
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	v, ok := s.st.getVersion(id)
	if !ok {
		return RuleVersion{}, ErrVersionNotFound
	}
	return *cloneVersion(v), nil
}

// CorrectionsOf 返回某条审计记录的纠正链（按确定顺序）。
func (s *System) CorrectionsOf(recordID RecordID) ([]Correction, error) {
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	if _, ok := s.st.getRecord(recordID); !ok {
		return nil, ErrRecordNotFound
	}
	return s.correctionsOfLocked(recordID), nil
}

func (s *System) correctionsOfLocked(recordID RecordID) []Correction {
	ids := s.st.byRecord[recordID]
	out := make([]Correction, 0, len(ids))
	for _, id := range ids {
		out = append(out, *s.st.corrections[id])
	}
	return out
}

func correctionIDs(cs []Correction) []CorrectionID {
	ids := make([]CorrectionID, 0, len(cs))
	for _, c := range cs {
		ids = append(ids, c.ID)
	}
	return ids
}

func cloneRecord(r *AuditRecord) *AuditRecord {
	cp := *r
	if r.Request.Attrs != nil {
		cp.Request.Attrs = make(map[string]string, len(r.Request.Attrs))
		for k, v := range r.Request.Attrs {
			cp.Request.Attrs[k] = v
		}
	}
	return &cp
}

func cloneVersion(v *RuleVersion) *RuleVersion {
	cp := *v
	cp.Content.Rules = make([]Rule, len(v.Content.Rules))
	for i, rule := range v.Content.Rules {
		cp.Content.Rules[i] = Rule{
			Effect:   rule.Effect,
			Subjects: append([]string(nil), rule.Subjects...),
			Targets:  append([]string(nil), rule.Targets...),
			Actions:  append([]string(nil), rule.Actions...),
		}
	}
	return &cp
}

func (s *System) log(e Event) {
	if s.logger == nil {
		return
	}
	e.At = s.now()
	s.logger.LogEvent(e)
}
