package cardinality

import (
	"fmt"
	"testing"
)

func keyOf(linkType, source string) BucketKey {
	return BucketKey{LinkType: linkType, Direction: Outgoing, SourceID: source}
}

func idsOf(links []Link) []string {
	out := make([]string, 0, len(links))
	for _, lk := range links {
		out = append(out, lk.ID)
	}
	return out
}

func pendingIDs(views []PendingView) []string {
	out := make([]string, 0, len(views))
	for _, v := range views {
		out = append(out, v.Link.ID)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func reasonSet(records []AuditRecord) map[Reason]bool {
	m := make(map[Reason]bool)
	for _, rec := range records {
		m[rec.Reason] = true
	}
	return m
}

func createN(t *testing.T, e *Engine, source string, n int) []string {
	t.Helper()
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("%s-L%d", source, i+1)
		res := e.CreateLink("owns", source, fmt.Sprintf("T-%s-%d", source, i+1), id)
		if !res.Accepted {
			t.Fatalf("create %s rejected unexpectedly: %s", id, res.RejectReason)
		}
		ids = append(ids, id)
	}
	return ids
}

func assertBucketsEquivalent(t *testing.T, a, b *Engine, k BucketKey) {
	t.Helper()
	if got, want := idsOf(a.ActiveLinks(k)), idsOf(b.ActiveLinks(k)); !equalStrings(got, want) {
		t.Fatalf("active mismatch: %v vs %v", got, want)
	}
	if got, want := pendingIDs(a.PendingLinks(k)), pendingIDs(b.PendingLinks(k)); !equalStrings(got, want) {
		t.Fatalf("pending mismatch: %v vs %v", got, want)
	}
	if a.RegisteredCount(k) != b.RegisteredCount(k) {
		t.Fatalf("registered mismatch: %d vs %d", a.RegisteredCount(k), b.RegisteredCount(k))
	}
	if a.EffectiveCapacity(k) != b.EffectiveCapacity(k) {
		t.Fatalf("capacity mismatch: %d vs %d", a.EffectiveCapacity(k), b.EffectiveCapacity(k))
	}
}

// TestAlternatingDowngradeUpgrade 覆盖多次连续下调与上调交替后
// 待处理集合与有效集合的最终状态核对，并核对逆序恢复。
func TestAlternatingDowngradeUpgrade(t *testing.T) {
	e := NewEngine(nil)
	k := keyOf("owns", "A")
	ids := createN(t, e, "A", 5)

	r := e.SetLimit(k, 3)
	if got := pendingIDs(e.PendingLinks(k)); !equalStrings(got, []string{ids[3], ids[4]}) {
		t.Fatalf("limit 3: pending=%v want %v,%v", got, ids[3], ids[4])
	}
	if !equalStrings(r.Marked, []string{ids[3], ids[4]}) {
		t.Fatalf("marked order = %v", r.Marked)
	}
	if e.PendingCount(k) != 2 || e.RegisteredCount(k) != 5 {
		t.Fatalf("pending=%d registered=%d", e.PendingCount(k), e.RegisteredCount(k))
	}

	r = e.SetLimit(k, 2)
	if got := pendingIDs(e.PendingLinks(k)); !equalStrings(got, []string{ids[2], ids[3], ids[4]}) {
		t.Fatalf("limit 2: pending=%v", got)
	}
	if !equalStrings(r.Marked, []string{ids[2]}) {
		t.Fatalf("marked = %v, want [%s]", r.Marked, ids[2])
	}

	// 标记顺序：第一批(L4,L5)、第二批(L3)；逆序恢复应为 L3 然后 L4。
	r = e.SetLimit(k, 4)
	if !equalStrings(r.Restored, []string{ids[2], ids[3]}) {
		t.Fatalf("restore order = %v, want %s,%s", r.Restored, ids[2], ids[3])
	}
	if got := idsOf(e.ActiveLinks(k)); !equalStrings(got, ids[:4]) {
		t.Fatalf("active=%v want first four", got)
	}
	if got := pendingIDs(e.PendingLinks(k)); !equalStrings(got, []string{ids[4]}) {
		t.Fatalf("pending=%v want last one", got)
	}

	r = e.SetLimit(k, 5)
	if !equalStrings(r.Restored, []string{ids[4]}) {
		t.Fatalf("final restore = %v", r.Restored)
	}
	if e.PendingCount(k) != 0 {
		t.Fatalf("pending should be empty, got %d", e.PendingCount(k))
	}
	if got := idsOf(e.ActiveLinks(k)); !equalStrings(got, ids) {
		t.Fatalf("active=%v want all in registration order", got)
	}

	ref := NewEngine(nil)
	createN(t, ref, "A", 5)
	ref.SetLimit(k, 5)
	assertBucketsEquivalent(t, e, ref, k)

	reasons := reasonSet(e.AuditLog())
	for _, want := range []Reason{ReasonMarkedExcess, ReasonPendingRestored} {
		if !reasons[want] {
			t.Fatalf("audit missing reason %s", want)
		}
	}
}

// TestDeterministicSelection 同一组输入重复执行，超额集合与标记依据完全一致。
func TestDeterministicSelection(t *testing.T) {
	run := func() []MarkBasis {
		e := NewEngine(nil)
		k := keyOf("owns", "D")
		createN(t, e, "D", 6)
		e.SetLimit(k, 2)
		e.SetLimit(k, 4)
		var bases []MarkBasis
		for _, rec := range e.AuditLog() {
			if rec.Reason == ReasonMarkedExcess {
				bases = append(bases, *rec.Basis)
			}
		}
		return bases
	}
	b1, b2 := run(), run()
	if len(b1) != len(b2) {
		t.Fatalf("basis count mismatch %d vs %d", len(b1), len(b2))
	}
	for i := range b1 {
		if b1[i].LimitAtMark != b2[i].LimitAtMark ||
			b1[i].RankAtMark != b2[i].RankAtMark ||
			b1[i].TotalAtMark != b2[i].TotalAtMark ||
			!equalStrings(b1[i].OrderedIDs, b2[i].OrderedIDs) {
			t.Fatalf("mark basis not deterministic:\n%+v\n%+v", b1[i], b2[i])
		}
	}
}

// TestCreateRejectedByNewLimit 下调后新建请求按当前有效上限被拒绝。
func TestCreateRejectedByNewLimit(t *testing.T) {
	e := NewEngine(nil)
	k := keyOf("owns", "R")
	ids := createN(t, e, "R", 3)
	e.SetLimit(k, 2)
	res := e.CreateLink("owns", "R", "T-new", "R-new")
	if res.Accepted || res.RejectReason != ReasonCreateRejected {
		t.Fatalf("got accepted=%v reason=%s", res.Accepted, res.RejectReason)
	}
	if e.RegisteredCount(k) != 3 {
		t.Fatalf("rejected create must not register, got %d", e.RegisteredCount(k))
	}

	e.SetLimit(k, 1) // active=L1；pending=L2,L3，有效集合已满。
	res = e.CreateLink("owns", "R", "T-new2", "R-new2")
	if res.Accepted {
		t.Fatal("create with full active set despite pending must be rejected")
	}

	// 删除 L2 空出的是“待处理”位置：容量 1 的唯一槽仍被 L1 占据，
	// L3 按确定性规则依旧超额，新链接继续被拒绝。
	if _, err := e.Finalize(ids[1], DispositionDelete); err != nil {
		t.Fatal(err)
	}
	if got := idsOf(e.ActiveLinks(k)); !equalStrings(got, []string{ids[0]}) {
		t.Fatalf("active after delete = %v", got)
	}
	if got := pendingIDs(e.PendingLinks(k)); !equalStrings(got, []string{ids[2]}) {
		t.Fatalf("pending after delete = %v", got)
	}
	res = e.CreateLink("owns", "R", "T-x", "R-x")
	if res.Accepted {
		t.Fatal("capacity still full after pending delete")
	}

	// 上限上调到 4：L3 恢复，且腾出 1 个有效槽，新建按新上限被接受。
	e.SetLimit(k, 4)
	res = e.CreateLink("owns", "R", "T-new3", "R-new3")
	if !res.Accepted {
		t.Fatalf("after limit raised, create should be accepted: %s", res.RejectReason)
	}
}

// TestRetainRaisesEffectiveLimit 保留处置提升有效容量并联动恢复。
func TestRetainRaisesEffectiveLimit(t *testing.T) {
	e := NewEngine(nil)
	k := keyOf("owns", "P")
	ids := createN(t, e, "P", 4)
	e.SetLimit(k, 1)

	res, err := e.Finalize(ids[3], DispositionRetain)
	if err != nil || !res.Kept || res.Reason != ReasonFinalRetain {
		t.Fatalf("retain result=%+v err=%v", res, err)
	}
	if e.EffectiveCapacity(k) != 4 {
		t.Fatalf("effective capacity = %d, want 4", e.EffectiveCapacity(k))
	}
	if got := idsOf(e.ActiveLinks(k)); !equalStrings(got, ids) {
		t.Fatalf("active after retain = %v, want all", got)
	}
	if e.PendingCount(k) != 0 {
		t.Fatalf("pending = %d, want 0", e.PendingCount(k))
	}
}

// TestRevokedObjectForcesDelete 对象撤销判定优先于请求保留的默认路径。
func TestRevokedObjectForcesDelete(t *testing.T) {
	e := NewEngine(nil)
	k := keyOf("owns", "V")
	ids := createN(t, e, "V", 3)
	e.SetLimit(k, 1)

	e.RevokeObject("T-V-3")
	res, err := e.Finalize(ids[2], DispositionRetain)
	if err != nil {
		t.Fatal(err)
	}
	if res.Kept || res.Applied != DispositionDelete || res.Reason != ReasonObjectRevoked {
		t.Fatalf("revoked object must force delete, got %+v", res)
	}
	lk, _ := e.GetLink(ids[2])
	if lk.State != StateDeleted {
		t.Fatalf("link state = %s", lk.State)
	}
	if !reasonSet(e.AuditLog())[ReasonObjectRevoked] {
		t.Fatal("audit missing force-delete-by-revocation reason")
	}
	// 容量仍为 1，L1 占据唯一有效槽，L2 依旧 pending。
	if got := idsOf(e.ActiveLinks(k)); !equalStrings(got, []string{ids[0]}) {
		t.Fatalf("active after forced delete = %v", got)
	}
	if got := pendingIDs(e.PendingLinks(k)); !equalStrings(got, []string{ids[1]}) {
		t.Fatalf("pending after forced delete = %v", got)
	}
}

// TestFinalizeRechecksCondition 转为保留时必须用当前状态重新校验对象有效性。
func TestFinalizeRechecksCondition(t *testing.T) {
	e := NewEngine(func(objectID string) bool { return objectID != "T-C-2" })
	k := keyOf("owns", "C")
	var ids []string
	for i := 0; i < 2; i++ {
		id := fmt.Sprintf("C-L%d", i+1)
		r := e.CreateLink("owns", "C", fmt.Sprintf("T-C-%d", i+1), id)
		if !r.Accepted {
			t.Fatal(r.RejectReason)
		}
		ids = append(ids, id)
	}
	e.SetLimit(k, 1)
	res, err := e.Finalize(ids[1], DispositionRetain)
	if err != nil {
		t.Fatal(err)
	}
	if res.Kept || res.Reason != ReasonObjectRevoked {
		t.Fatalf("retain must be re-validated and forced to delete, got %+v", res)
	}
}

// TestDerivedStateSuspensionAndVisibility 派生状态在待处理期间保留但明确过期；
// 最终删除则清理，恢复则重新可信。
func TestDerivedStateSuspensionAndVisibility(t *testing.T) {
	e := NewEngine(nil)
	k := keyOf("owns", "G")
	ids := createN(t, e, "G", 2)
	if err := e.RegisterDerived("D1", ids[1], "materialized-summary"); err != nil {
		t.Fatal(err)
	}

	e.SetLimit(k, 1)
	view, ok := e.QueryDerived("D1")
	if !ok || view.Fresh || view.Notice == "" {
		t.Fatalf("pending derived must be stale with notice: %+v ok=%v", view, ok)
	}
	if view.State.Payload != "materialized-summary" || view.State.Cleared {
		t.Fatal("suspect derived state must be retained, not cleared or emptied")
	}
	pv := e.PendingLinks(k)
	if len(pv) != 1 || !equalStrings(pv[0].DependentsStale, []string{"D1"}) || !pv[0].Stale {
		t.Fatalf("pending view must expose stale dependents: %+v", pv)
	}

	e.SetLimit(k, 2)
	view, _ = e.QueryDerived("D1")
	if !view.Fresh || view.State.Suspect {
		t.Fatalf("restored derived must be fresh: %+v", view)
	}

	e.SetLimit(k, 1)
	if _, err := e.Finalize(ids[1], DispositionDelete); err != nil {
		t.Fatal(err)
	}
	view, _ = e.QueryDerived("D1")
	if view.Fresh || !view.State.Cleared || view.State.Payload != "" {
		t.Fatalf("cleared derived must be explicitly non-fresh and emptied: %+v", view)
	}
}

// TestTrajectoryIndependence 反复调整轨迹不影响最终状态，
// 且对同一条 pending 链接做相同处置时去向一致。
func TestTrajectoryIndependence(t *testing.T) {
	k := keyOf("owns", "J")
	build := func() (*Engine, []string) {
		e := NewEngine(nil)
		return e, createN(t, e, "J", 6)
	}

	e1, ids := build()
	e1.SetLimit(k, 3)
	e1.SetLimit(k, 1)
	e1.SetLimit(k, 5)
	e1.SetLimit(k, 2)
	e1.SetLimit(k, 4)

	e2, _ := build()
	e2.SetLimit(k, 4)

	assertBucketsEquivalent(t, e1, e2, k)
	if got := pendingIDs(e1.PendingLinks(k)); !equalStrings(got, []string{ids[4], ids[5]}) {
		t.Fatalf("trajectory case pending=%v", got)
	}

	r1, err := e1.Finalize(ids[4], DispositionRetain)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := e2.Finalize(ids[4], DispositionRetain)
	if err != nil {
		t.Fatal(err)
	}
	if r1.Kept != r2.Kept || r1.Applied != r2.Applied {
		t.Fatalf("final disposition must depend only on mark basis + last limit: %+v vs %+v", r1, r2)
	}
	assertBucketsEquivalent(t, e1, e2, k)

	// 对剩余 pending 做删除处置，两引擎仍一致。
	for _, id := range pendingIDs(e1.PendingLinks(k)) {
		if _, err := e1.Finalize(id, DispositionDelete); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range pendingIDs(e2.PendingLinks(k)) {
		if _, err := e2.Finalize(id, DispositionDelete); err != nil {
			t.Fatal(err)
		}
	}
	assertBucketsEquivalent(t, e1, e2, k)
}
