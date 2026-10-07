package ontology

import (
	"encoding/json"
	"strings"
	"testing"
)

// setupWorksAt 构造：Person/Company 已注册，worksAt 上 p1 拥有两条链接，
// 随后发生两次基数约束版本调整。返回各关键记录时刻。
func setupWorksAt(t *testing.T) (s *Store, typeReady, v2at, v3at int64) {
	t.Helper()
	s = NewStore()
	s.RegisterObjectType("Person")
	s.RegisterObjectType("Company")
	typeReady = s.Now()
	if err := s.RegisterLinkType(LinkTypeDef{ID: "worksAt", LeftType: "Person", RightType: "Company"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateLink("worksAt", "p1", "c1", 0); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateLink("worksAt", "p1", "c2", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AdjustCardinality("worksAt", Cardinality{Min: 0, Max: 1}, unconstrained); err != nil {
		t.Fatal(err)
	}
	v2at = s.Now()
	if _, err := s.AdjustCardinality("worksAt", Cardinality{Min: 0, Max: 2}, unconstrained); err != nil {
		t.Fatal(err)
	}
	v3at = s.Now()
	return s, typeReady, v2at, v3at
}

// TestAuditSegmentsAcrossVersions 审计区间跨越两次约束版本调整时，
// 结果必须按版本分段，且各段使用当时生效的版本判定。
func TestAuditSegmentsAcrossVersions(t *testing.T) {
	s, typeReady, v2at, v3at := setupWorksAt(t)
	now := s.Now()

	rep, err := s.Audit(AuditRequest{
		LinkType: "worksAt", RecordFrom: typeReady, RecordTo: now,
		ValidAt: 0, ExpectedVersion: 3,
	})
	if err != nil || rep.Err != nil {
		t.Fatalf("unexpected audit failure: %v %v", err, rep.Err)
	}
	if len(rep.Segments) != 3 {
		t.Fatalf("expected 3 segments, got %+v", rep.Segments)
	}
	// 段边界与版本。
	if rep.Segments[0].Version != 1 || rep.Segments[1].Version != 2 || rep.Segments[2].Version != 3 {
		t.Fatalf("segment versions wrong: %+v", rep.Segments)
	}
	if rep.Segments[1].From != v2at || rep.Segments[1].To != v3at-1 {
		t.Fatalf("segment 1 bounds wrong: %+v", rep.Segments[1])
	}
	// v1（不限）与 v3（Max=2）段无违反；v2（Max=1）段 p1 违反。
	if len(rep.Segments[0].Violations) != 0 || len(rep.Segments[2].Violations) != 0 {
		t.Fatalf("segments 0/2 should be clean: %+v", rep.Segments)
	}
	v := rep.Segments[1].Violations
	if len(v) != 1 || v[0].Object != "p1" || v[0].Degree != 2 || v[0].Limit.Max != 1 {
		t.Fatalf("segment 1 violations wrong: %+v", v)
	}
	if v[0].From != v2at || v[0].To != v3at {
		t.Fatalf("violation range wrong: %+v", v[0])
	}
}

// TestAuditErrorPriority 多类错误条件同时成立时只报告最高优先级一类。
func TestAuditErrorPriority(t *testing.T) {
	s, typeReady, _, _ := setupWorksAt(t)
	now := s.Now()

	// 同时满足 1/2/3：区间矛盾 + 类型不存在 + 版本作废 → 报区间矛盾。
	rep, _ := s.Audit(AuditRequest{
		LinkType: "worksAt", RecordFrom: now + 1, RecordTo: now,
		ValidAt: 0, ExpectedVersion: 99,
	})
	if rep.Err == nil || rep.Err.Class != ErrorContradictoryInterval {
		t.Fatalf("expected contradictory-interval, got %+v", rep.Err)
	}
	// 同时满足 2/3：类型在 RecordFrom 时刻尚不存在 + 版本作废 → 报类型缺失。
	rep, _ = s.Audit(AuditRequest{
		LinkType: "worksAt", RecordFrom: typeReady - 1, RecordTo: now,
		ValidAt: 0, ExpectedVersion: 99,
	})
	if rep.Err == nil || rep.Err.Class != ErrorObjectTypeMissing {
		t.Fatalf("expected object-type-missing, got %+v", rep.Err)
	}
	// 仅满足 3：版本作废。
	rep, _ = s.Audit(AuditRequest{
		LinkType: "worksAt", RecordFrom: typeReady, RecordTo: now,
		ValidAt: 0, ExpectedVersion: 2,
	})
	if rep.Err == nil || rep.Err.Class != ErrorVersionSuperseded {
		t.Fatalf("expected version-superseded, got %+v", rep.Err)
	}
	// 仅满足 4：对称链接镜像缺损。
	if err := s.RegisterLinkType(LinkTypeDef{ID: "friend", LeftType: "Person", RightType: "Person", Symmetric: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordFact("friend", FactInput{
		Kind: FactCreate, Left: "p1", Right: "p2", ValidFrom: 0, Source: EndpointLeft,
	}); err != nil {
		t.Fatal(err)
	}
	rep, _ = s.Audit(AuditRequest{
		LinkType: "friend", RecordFrom: typeReady, RecordTo: s.Now(),
		ValidAt: 0, ExpectedVersion: 1,
	})
	if rep.Err == nil || rep.Err.Class != ErrorMirrorInconsistency {
		t.Fatalf("expected mirror-inconsistency, got %+v", rep.Err)
	}
	// 全部条件解除后审计成功。
	rep, _ = s.Audit(AuditRequest{
		LinkType: "worksAt", RecordFrom: typeReady, RecordTo: now,
		ValidAt: 0, ExpectedVersion: 3,
	})
	if rep.Err != nil {
		t.Fatalf("expected success, got %+v", rep.Err)
	}
}

// TestAuditErrorsDoNotMutateHistory 任一错误发生都不得对链接历史轨迹
// 产生任何可观察的改动（事实日志、快照、版本、记录时钟全部不变）。
func TestAuditErrorsDoNotMutateHistory(t *testing.T) {
	s, typeReady, _, _ := setupWorksAt(t)
	now := s.Now()

	snapshotOf := func() [4]int64 {
		s.mu.Lock()
		defer s.mu.Unlock()
		st := s.links["worksAt"]
		return [4]int64{int64(len(st.facts)), int64(len(st.snaps)), int64(len(st.versions)), s.seq}
	}
	before := snapshotOf()

	bad := []AuditRequest{
		{LinkType: "worksAt", RecordFrom: now + 1, RecordTo: now, ExpectedVersion: 3},
		{LinkType: "worksAt", RecordFrom: typeReady - 1, RecordTo: now, ExpectedVersion: 3},
		{LinkType: "worksAt", RecordFrom: typeReady, RecordTo: now, ExpectedVersion: 1},
		{LinkType: "worksAt", RecordFrom: typeReady, RecordTo: now, ExpectedVersion: 3}, // 成功路径同样只读
	}
	for _, req := range bad {
		if _, err := s.Audit(req); err != nil {
			t.Fatal(err)
		}
	}
	if after := snapshotOf(); after != before {
		t.Fatalf("audit mutated history: before=%v after=%v", before, after)
	}
}

// TestDecisionLog 每次判定的输入、所依据的约束版本与结论都必须落日志。
func TestDecisionLog(t *testing.T) {
	s, typeReady, _, _ := setupWorksAt(t)
	now := s.Now()

	okReq := AuditRequest{LinkType: "worksAt", RecordFrom: typeReady, RecordTo: now, ValidAt: 0, ExpectedVersion: 3}
	if _, err := s.Audit(okReq); err != nil {
		t.Fatal(err)
	}
	badReq := AuditRequest{LinkType: "worksAt", RecordFrom: now + 1, RecordTo: now, ExpectedVersion: 3}
	if _, err := s.Audit(badReq); err != nil {
		t.Fatal(err)
	}

	log := s.DecisionLog()
	if len(log) != 2 {
		t.Fatalf("expected 2 audit records, got %d", len(log))
	}
	if log[0].Request != okReq || log[0].ErrorClass != ErrorNone {
		t.Fatalf("record 0 mismatch: %+v", log[0])
	}
	// 判定所依据的版本必须逐段可查。
	versions := map[int]bool{}
	for _, seg := range log[0].Segments {
		versions[seg.Version] = true
	}
	if !versions[1] || !versions[2] || !versions[3] {
		t.Fatalf("decision log missing per-segment versions: %+v", log[0].Segments)
	}
	if log[1].Request != badReq || log[1].ErrorClass != ErrorContradictoryInterval {
		t.Fatalf("record 1 mismatch: %+v", log[1])
	}
	if log[0].Seq != 1 || log[1].Seq != 2 {
		t.Fatalf("audit record seq not monotonic: %+v", log)
	}
}

// TestAuditOutputHidesRecordSource 审计输出（含 JSON 序列化形式）
// 不得暴露任何记录来源信息。
func TestAuditOutputHidesRecordSource(t *testing.T) {
	s := newTestStore(t)
	// 用单端记录制造缺损，确保输出处于"最丰富"状态。
	if err := s.RecordFact("friend", FactInput{
		Kind: FactCreate, Left: "u1", Right: "u2", ValidFrom: 0, Source: EndpointLeft,
	}); err != nil {
		t.Fatal(err)
	}
	rep, err := s.Audit(AuditRequest{
		LinkType: "friend", RecordFrom: 1, RecordTo: s.Now(),
		ValidAt: 0, ExpectedVersion: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	replay := s.Replay("friend", s.Now(), 0)
	for name, v := range map[string]any{"report": rep, "replay": replay, "log": s.DecisionLog()} {
		buf, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		low := strings.ToLower(string(buf))
		for _, banned := range []string{"source", "endpoint"} {
			if strings.Contains(low, banned) {
				t.Fatalf("%s JSON leaks record source (%q): %s", name, banned, buf)
			}
		}
	}
	// 语义层面：缺损标记不指明来源端。
	if !replay.Links[0].SymmetryDeficit {
		t.Fatal("expected symmetry deficit flag on single-end record")
	}
}

// TestReplayCostIndependentOfHistory 回放开销不得随累计事实总量线性增长：
// 同一历史记录时刻的回放步数，在之后追加大量事实与版本调整前后必须一致。
func TestReplayCostIndependentOfHistory(t *testing.T) {
	s := newTestStore(t)
	const lt = LinkTypeID("worksAt")

	for i := 0; i < 10; i++ {
		if err := s.CreateLink(lt, ObjectID("p0"), ObjectID('a'+rune(i)), 0); err != nil {
			t.Fatal(err)
		}
	}
	rt := s.Now()
	s.Replay(lt, rt, 0)
	stepsBefore := s.Stats(lt).LastReplaySteps

	// 追加 5000 条事实与若干版本调整（历史大幅膨胀）。
	for i := 0; i < 5000; i++ {
		if err := s.CreateLink(lt, ObjectID("q"+string(rune('a'+i%26))), ObjectID('A'+rune(i%26)), 0); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 20; i++ {
		if _, err := s.AdjustCardinality(lt, Cardinality{Max: 100 + i}, unconstrained); err != nil {
			t.Fatal(err)
		}
	}
	st := s.Stats(lt)
	if st.Facts != 5010 || st.Snapshots == 0 {
		t.Fatalf("unexpected stats: %+v", st)
	}

	s.Replay(lt, rt, 0)
	stepsAfter := s.Stats(lt).LastReplaySteps
	// 增长只允许来自快照索引的二分定位（对数级），
	// 不得随累计事实总量（5010 条）线性增长。
	if grew := stepsAfter - stepsBefore; grew > int64(lenBin(st.Snapshots))+1 {
		t.Fatalf("replay steps grew super-logarithmically with history: before=%d after=%d snapshots=%d",
			stepsBefore, stepsAfter, st.Snapshots)
	}
	// 上界自证：步数不超过 log2(快照数)+1 次二分 + 一个步长的增量 + 当时存活状态数。
	s.Replay(lt, s.Now(), 0)
	latest := s.Stats(lt).LastReplaySteps
	bound := int64(64*(lenBin(s.Stats(lt).Snapshots)+1)) + int64(snapshotStride) + 5010
	if latest > bound {
		t.Fatalf("replay steps %d exceed structural bound %d", latest, bound)
	}
}

func lenBin(n int) int {
	b := 0
	for n > 0 {
		n >>= 1
		b++
	}
	return b
}
