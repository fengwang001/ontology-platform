package audit

import (
	"errors"
	"strconv"
	"sync"
)

// AppendCorrectionInput is the caller supplied portion of a correction.
type AppendCorrectionInput struct {
	ID      string
	AuditID string
	Reason  string
	// CorrectedAllow is the human-adjudicated replacement conclusion.
	CorrectedAllow bool
	// Optional explicit predecessor. Zero values mean "the current chain head
	// (or the original audit when the chain is empty)".
	TargetID   string
	TargetType CorrectionTarget
}

// Service is the audit/replay subsystem. All methods are linearisable: any
// concurrent execution is equivalent to some serial order of the calls.
type Service struct {
	mu     sync.Mutex
	store  *Store
	engine Engine
	logger Logger
}

// NewService creates a Service backed by a fresh in-memory store.
func NewService(logger Logger) *Service {
	return &Service{store: NewStore(), engine: CanonicalEngine{}, logger: logger}
}

func (s *Service) log(method string, input, output any, err error, basis *DecisionBasis) {
	if s.logger == nil {
		return
	}
	entry := CallLogEntry{Method: method, Input: input, Output: output, Basis: basis}
	if err != nil {
		entry.Error = err.Error()
		entry.ErrorCode = errorCode(err)
	}
	s.logger.Log(entry)
}

func errorCode(err error) string {
	for _, cand := range []error{
		ErrAuditNotFound, ErrVersionNotFound, ErrVersionTampered, ErrAuditTampered,
		ErrCorrectionTargetMissing, ErrCorrectionTampered, ErrCorrectionConflict,
		ErrReplayMatch, ErrInvalidInput, ErrAlreadyExists,
	} {
		if errors.Is(err, cand) {
			return cand.Error()
		}
	}
	return "ERROR"
}

// CommitVersion appends a new immutable rule version. ParentID must name the
// current head version (or be empty for the first version), which makes the
// total order of versions unique and deterministic.
func (s *Service) CommitVersion(in RuleVersionInput) (v RuleVersion, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() { s.log("CommitVersion", in, v, err, nil) }()

	if in.ID == "" {
		return RuleVersion{}, ErrInvalidInput
	}
	if _, exists := s.store.versionsByID[in.ID]; exists {
		return RuleVersion{}, ErrAlreadyExists
	}
	head := s.store.headVersion()
	switch {
	case head == nil:
		if in.ParentID != "" {
			return RuleVersion{}, ErrVersionNotFound
		}
	default:
		if in.ParentID == "" || in.ParentID != head.ID {
			return RuleVersion{}, ErrInvalidInput
		}
	}
	for _, r := range in.Rules.Rules {
		if r.ID == "" || (r.Effect != EffectAllow && r.Effect != EffectDeny) {
			return RuleVersion{}, ErrInvalidInput
		}
	}
	contentHash, _, err := hashVersionContent(in)
	if err != nil {
		return RuleVersion{}, ErrInvalidInput
	}
	seq := int64(s.store.VersionCount()) + 1
	v = RuleVersion{
		ID:            in.ID,
		Seq:           seq,
		ParentID:      in.ParentID,
		Rules:         cloneRules(in.Rules),
		CreatedAt:     now(),
		ContentHash:   contentHash,
		PrevChainHash: chainHead(head),
	}
	v.ChainHash, err = hashVersionChain(v)
	if err != nil {
		return RuleVersion{}, ErrInvalidInput
	}
	s.store.appendVersion(v)
	return v, nil
}

func chainHead(head *RuleVersion) string {
	if head == nil {
		return ""
	}
	return head.ChainHash
}

func cloneRules(rs RuleSet) RuleSet {
	out := RuleSet{Rules: make([]Rule, len(rs.Rules))}
	for i, r := range rs.Rules {
		out.Rules[i] = Rule{
			ID:       r.ID,
			Subjects: append([]string(nil), r.Subjects...),
			Targets:  append([]string(nil), r.Targets...),
			Actions:  append([]string(nil), r.Actions...),
			Effect:   r.Effect,
			Order:    r.Order,
		}
	}
	return out
}

// GetVersion returns a committed rule version.
func (s *Service) GetVersion(id string) (v RuleVersion, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() { s.log("GetVersion", map[string]string{"id": id}, v, err, nil) }()
	stored, _, ok := s.store.getVersion(id)
	if !ok {
		return RuleVersion{}, ErrVersionNotFound
	}
	return stored, nil
}

// ListVersions returns all rule versions in commit order.
func (s *Service) ListVersions() []RuleVersion {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]RuleVersion, 0, s.store.VersionCount())
	for _, id := range s.store.versionOrder {
		out = append(out, s.store.versionsByID[id])
	}
	return out
}

// RecordAudit runs the current canonical adjudication and appends the audit
// record.
func (s *Service) RecordAudit(in AuditInput) (AuditRecord, error) {
	return s.recordAudit(in, s.engine)
}

// RecordAuditWithEngine records a decision using an explicitly supplied
// adjudication engine (used when reconstructing decisions made by a
// since-discovered-defective historical engine).
func (s *Service) RecordAuditWithEngine(in AuditInput, engine Engine) (AuditRecord, error) {
	if engine == nil {
		engine = CanonicalEngine{}
	}
	return s.recordAudit(in, engine)
}

func (s *Service) recordAudit(in AuditInput, engine Engine) (rec AuditRecord, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() {
		basis := &DecisionBasis{
			RuleVersionID: rec.RuleVersionID, RuleVersionSeq: 0,
			AuditID: rec.ID, AuditSeq: rec.Seq,
		}
		s.log("RecordAudit", in, rec, err, basis)
	}()

	if in.Subject == "" || in.Target == "" || in.Action == "" || in.RuleVersionID == "" {
		return AuditRecord{}, ErrInvalidInput
	}
	v, _, ok := s.store.getVersion(in.RuleVersionID)
	if !ok {
		return AuditRecord{}, ErrVersionNotFound
	}
	seq := int64(s.store.AuditCount()) + 1
	rec = AuditRecord{
		ID:        "audit-" + strconv.FormatInt(seq, 10),
		Seq:       seq,
		Subject:   in.Subject,
		Target:    in.Target,
		Action:    in.Action,
		Content:   in.Content,
		DecidedAt: now(),
		Allow: engine.Adjudicate(v.Rules, AccessRequest{
			Subject: in.Subject, Target: in.Target, Action: in.Action, Content: in.Content,
		}),
		RuleVersionID: v.ID,
		EngineTag:     engine.Tag(),
		PrevHash:      s.auditChainHead(),
	}
	rec.RecordHash, err = hashAudit(rec)
	if err != nil {
		return AuditRecord{}, ErrInvalidInput
	}
	s.store.appendAudit(rec)
	return rec, nil
}

func (s *Service) auditChainHead() string {
	if s.store.AuditCount() == 0 {
		return ""
	}
	all := s.store.listAudits()
	return all[len(all)-1].RecordHash
}

// GetAudit returns one audit record.
func (s *Service) GetAudit(id string) (a AuditRecord, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() { s.log("GetAudit", map[string]string{"id": id}, a, err, nil) }()
	rec, _, ok := s.store.getAudit(id)
	if !ok {
		return AuditRecord{}, ErrAuditNotFound
	}
	return rec, nil
}

// ListAudits returns all audit records in commit order.
func (s *Service) ListAudits() []AuditRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.listAudits()
}

// Replay rebuilds the decision of one audit record solely from the rule
// version the record names and the record's own inputs. It uses no caches and
// reads exactly one rule version.
func (s *Service) Replay(auditID string) (report ReplayReport, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() {
		basis := &DecisionBasis{AuditID: auditID, RuleVersionID: report.RuleVersionID}
		s.log("Replay", map[string]string{"audit_id": auditID}, report, err, basis)
	}()
	return s.replayLocked(auditID)
}

// replayLocked performs the replay without locking or logging. Error
// reporting follows the fixed precedence: audit missing < version missing <
// version tampered < audit tampered.
func (s *Service) replayLocked(auditID string) (ReplayReport, error) {
	rec, auditBytes, ok := s.store.getAudit(auditID)
	if !ok {
		return ReplayReport{}, ErrAuditNotFound
	}
	report := ReplayReport{
		AuditID:        rec.ID,
		RuleVersionID:  rec.RuleVersionID,
		OriginalAllow:  rec.Allow,
		EngineTag:      rec.EngineTag,
		AuditBytesRead: auditBytes,
	}
	version, versionBytes, vok := s.store.getVersion(rec.RuleVersionID)
	if !vok {
		return report, ErrVersionNotFound
	}
	report.VersionsTouched = 1
	report.VersionBytesRead = versionBytes
	if verr := verifyVersionContent(version); verr != nil {
		report.Outcome = ReplayIntegrityVersionTampered
		return report, verr
	}
	if verr := verifyAuditRecord(rec); verr != nil {
		report.Outcome = ReplayIntegrityAuditTampered
		return report, verr
	}
	replayed := CanonicalEngine{}.Adjudicate(version.Rules, AccessRequest{
		Subject: rec.Subject, Target: rec.Target, Action: rec.Action, Content: rec.Content,
	})
	report.ReplayedAllow = replayed
	if replayed == rec.Allow {
		report.Outcome = ReplayMatch
	} else {
		report.Outcome = ReplayMismatchDefectiveOriginal
	}
	return report, nil
}

// correctionsForAudit returns corrections pointing at one audit, commit order,
// after verifying every record and link in the chain.
func (s *Service) correctionsForAudit(auditID string) ([]Correction, error) {
	var chain []Correction
	for _, c := range s.store.listCorrections() {
		if c.AuditID != auditID {
			continue
		}
		if err := verifyCorrection(c); err != nil {
			return nil, err
		}
		chain = append(chain, c)
	}
	return chain, nil
}

// ListCorrections returns all corrections for one audit record, commit order.
func (s *Service) ListCorrections(auditID string) (list []Correction, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() { s.log("ListCorrections", map[string]string{"audit_id": auditID}, list, err, nil) }()
	if _, _, ok := s.store.getAudit(auditID); !ok {
		return nil, ErrAuditNotFound
	}
	list, err = s.correctionsForAudit(auditID)
	return list, err
}

// AppendCorrection replays the original decision and appends an independent
// correction record. It refuses every case that is not a defective-original
// mismatch; nothing existing is ever mutated.
func (s *Service) AppendCorrection(in AppendCorrectionInput) (corr Correction, report ReplayReport, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() {
		basis := &DecisionBasis{
			AuditID: in.AuditID, RuleVersionID: report.RuleVersionID,
			CorrectionID: corr.ID, CorrectionSeq: corr.Seq,
		}
		s.log("AppendCorrection", in, map[string]any{"correction": corr, "replay": report}, err, basis)
	}()

	rec, _, ok := s.store.getAudit(in.AuditID)
	if !ok {
		return Correction{}, ReplayReport{}, ErrAuditNotFound
	}
	report, err = s.replayLocked(in.AuditID)
	if err != nil {
		return Correction{}, report, err
	}
	chain, err := s.correctionsForAudit(in.AuditID)
	if err != nil {
		return Correction{}, report, err
	}

	// Resolve and validate the direct predecessor in fixed precedence order.
	targetType := in.TargetType
	targetID := in.TargetID
	var headTargetType CorrectionTarget
	var headTargetID string
	var headConclusion bool
	if len(chain) == 0 {
		headTargetType = CorrectionTargetAudit
		headTargetID = rec.ID
		headConclusion = rec.Allow
	} else {
		head := chain[len(chain)-1]
		headTargetType = CorrectionTargetCorrection
		headTargetID = head.ID
		headConclusion = head.CorrectedAllow
	}
	if targetType == "" && targetID == "" {
		targetType = headTargetType
		targetID = headTargetID
	}
	targetExists := false
	switch targetType {
	case CorrectionTargetAudit:
		_, _, targetExists = s.store.getAudit(targetID)
	case CorrectionTargetCorrection:
		_, targetExists = s.store.getCorrection(targetID)
		if targetExists {
			t, _ := s.store.getCorrection(targetID)
			targetExists = t.AuditID == rec.ID
		}
	default:
		return Correction{}, report, ErrInvalidInput
	}
	if !targetExists {
		return Correction{}, report, ErrCorrectionTargetMissing
	}
	if targetType != headTargetType || targetID != headTargetID {
		return Correction{}, report, ErrCorrectionConflict
	}
	if in.CorrectedAllow == headConclusion {
		return Correction{}, report, ErrCorrectionConflict
	}
	if len(chain) == 0 && report.Outcome == ReplayMatch {
		return Correction{}, report, ErrReplayMatch
	}
	if in.ID == "" {
		return Correction{}, report, ErrInvalidInput
	}
	if _, exists := s.store.getCorrection(in.ID); exists {
		return Correction{}, report, ErrAlreadyExists
	}

	seq := int64(s.store.CorrectionCount()) + 1
	corr = Correction{
		ID:             in.ID,
		Seq:            seq,
		AuditID:        rec.ID,
		TargetType:     targetType,
		TargetID:       targetID,
		CorrectedAllow: in.CorrectedAllow,
		Reason:         in.Reason,
		CreatedAt:      now(),
		PrevHash:       s.correctionChainHead(),
	}
	corr.RecordHash, err = hashCorrection(corr)
	if err != nil {
		return Correction{}, report, ErrInvalidInput
	}
	s.store.appendCorrection(corr)
	return corr, report, nil
}

func (s *Service) correctionChainHead() string {
	if s.store.CorrectionCount() == 0 {
		return ""
	}
	all := s.store.listCorrections()
	return all[len(all)-1].RecordHash
}

// Legality returns the three-way legality view for one audit record. The
// answer depends only on committed state, never on when the query is issued.
func (s *Service) Legality(auditID string) (view LegalityView, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer func() {
		basis := &DecisionBasis{AuditID: auditID}
		if view.ActiveCorrection != nil {
			basis.CorrectionID = view.ActiveCorrection.ID
		}
		s.log("Legality", map[string]string{"audit_id": auditID}, view, err, basis)
	}()

	rec, _, ok := s.store.getAudit(auditID)
	if !ok {
		return LegalityView{}, ErrAuditNotFound
	}
	if err = verifyAuditRecord(rec); err != nil {
		return LegalityView{}, err
	}
	chain, err := s.correctionsForAudit(auditID)
	if err != nil {
		return LegalityView{}, err
	}
	view = LegalityView{AuditID: rec.ID, OriginalAllow: rec.Allow, EffectiveAllow: rec.Allow}
	if len(chain) > 0 {
		head := chain[len(chain)-1]
		view.ActiveCorrection = &head
		view.EffectiveAllow = head.CorrectedAllow
	}
	if view.EffectiveAllow != rec.Allow {
		view.Kind = LegalityCorrected
	} else if rec.Allow {
		view.Kind = LegalityOriginalAllowed
	} else {
		view.Kind = LegalityOriginalDenied
	}
	return view, nil
}
