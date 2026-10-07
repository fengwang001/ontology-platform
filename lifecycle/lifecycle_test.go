package lifecycle

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func newTestCoordinator(t *testing.T) (*Coordinator, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	c := New(WithLogWriter(&buf))
	must(t, c.UpsertPermission(&PermissionEntry{ID: "p-del", Grants: []string{ActionDelete}, Version: 1}))
	must(t, c.UpsertPermission(&PermissionEntry{ID: "p-rev", Grants: []string{ActionRevive}, Version: 1}))
	must(t, c.UpsertPermission(&PermissionEntry{ID: "p-both", Grants: []string{ActionDelete, ActionRevive}, Version: 1}))
	must(t, c.UpsertPermission(&PermissionEntry{ID: "p-none", Grants: nil, Version: 1}))
	must(t, c.UpsertLinkType(&LinkType{ID: "lt-fragile", Behavior: LinkInvalidatesWithEndpoint}))
	must(t, c.UpsertLinkType(&LinkType{ID: "lt-independent", Behavior: LinkKeepsIndependent}))
	return c, &buf
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertErrIs(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("want error %v, got %v", target, err)
	}
}

func setupObjectWithLinks(t *testing.T, c *Coordinator) {
	t.Helper()
	must(t, c.CreateObject("a", "creator"))
	must(t, c.CreateObject("b", "creator"))
	must(t, c.CreateObject("x", "creator"))
	must(t, c.AddLink(&LinkRecord{ID: "L1", TypeID: "lt-fragile", SourceID: "a", TargetID: "b"}))
	must(t, c.AddLink(&LinkRecord{ID: "L2", TypeID: "lt-independent", SourceID: "a", TargetID: "b"}))
	must(t, c.AddLink(&LinkRecord{ID: "L3", TypeID: "lt-fragile", SourceID: "x", TargetID: "a"}))
}

// 多次删除复活循环：每段区间唯一标识，审计事件不得跨区间混淆。
func TestIntervalProvenanceUniqueAcrossCycles(t *testing.T) {
	c, _ := newTestCoordinator(t)
	setupObjectWithLinks(t, c)

	var deleteTicks []int64
	for i := 0; i < 4; i++ {
		must(t, c.Write("a", "u", "write-in-interval"))
		must(t, c.DeleteObject("a", "u", "p-del"))
		rep, err := c.Audit("a")
		must(t, err)
		last := rep.Events[len(rep.Events)-1]
		if last.Kind != EventDelete {
			t.Fatalf("want delete event, got %s", last.Kind)
		}
		deleteTicks = append(deleteTicks, last.At)

		must(t, c.ReviveObject("a", "u", "p-rev", last.At, false, nil))
	}
	must(t, c.DeleteObject("a", "u", "p-del"))

	rep, err := c.Audit("a")
	must(t, err)

	if len(rep.Intervals) != 5 {
		t.Fatalf("want 5 intervals, got %d", len(rep.Intervals))
	}
	ids := map[string]bool{}
	var prevEnd int64
	for i, iv := range rep.Intervals {
		if ids[iv.ID] {
			t.Fatalf("duplicate interval id %s", iv.ID)
		}
		ids[iv.ID] = true
		if iv.Seq != int64(i+1) {
			t.Fatalf("interval seq mismatch: %d", iv.Seq)
		}
		if iv.StartedAt <= prevEnd && i > 0 {
			t.Fatalf("intervals overlap: %+v prevEnd=%d", iv, prevEnd)
		}
		prevEnd = iv.EndedAt
	}
	// 每个事件必须归属到存在的区间，且区间内事件的时点落在区间边界内。
	byInterval := map[string][]*Event{}
	for _, ev := range rep.Events {
		if !ids[ev.IntervalID] {
			t.Fatalf("event %d references unknown interval %s", ev.Seq, ev.IntervalID)
		}
		byInterval[ev.IntervalID] = append(byInterval[ev.IntervalID], ev)
	}
	for _, iv := range rep.Intervals {
		for _, ev := range byInterval[iv.ID] {
			if ev.At < iv.StartedAt || (iv.EndedAt != 0 && ev.At > iv.EndedAt) {
				t.Fatalf("event at %d outside interval %s [%d,%d]", ev.At, iv.ID, iv.StartedAt, iv.EndedAt)
			}
		}
	}
	// 复活事件必须精确指向上一次删除时点，形成不可跳环的溯源链。
	var revives int
	for _, ev := range rep.Events {
		if ev.Kind == EventRevive {
			revives++
			if ev.TargetDeleteAt != deleteTicks[revives-1] {
				t.Fatalf("revive #%d targets %d, want %d", revives, ev.TargetDeleteAt, deleteTicks[revives-1])
			}
		}
	}
	if revives != 4 {
		t.Fatalf("want 4 revives, got %d", revives)
	}
}

// 两类链接的失效行为差异：第一类随删除失效并可在复活时恢复；第二类保持可用。
func TestTwoLinkBehaviorsAndRestore(t *testing.T) {
	c, _ := newTestCoordinator(t)
	setupObjectWithLinks(t, c)

	must(t, c.DeleteObject("a", "u", "p-del"))

	if got := c.links["L1"].Status; got != LinkInvalidated {
		t.Fatalf("L1 should be invalidated, got %d", got)
	}
	if got := c.links["L2"].Status; got != LinkAvailable {
		t.Fatalf("L2 independent link must stay available, got %d", got)
	}
	if got := c.links["L3"].Status; got != LinkInvalidated {
		t.Fatalf("L3 (reverse incident) should be invalidated, got %d", got)
	}
	deleteAt := c.links["L1"].InvalidatedAt

	// 仅恢复显式满足条件的 L1；L3 同样满足条件但未列入则不恢复。
	must(t, c.ReviveObject("a", "u", "p-rev", deleteAt, true, []string{"L1"}))
	if c.links["L1"].Status != LinkAvailable || c.links["L1"].InvalidatedAt != 0 {
		t.Fatalf("L1 should be restored: %+v", c.links["L1"])
	}
	if c.links["L3"].Status != LinkInvalidated {
		t.Fatalf("L3 must remain invalidated when not requested")
	}
	if c.links["L2"].Status != LinkAvailable {
		t.Fatalf("L2 must always stay available")
	}
}

// 更早失效的链接（对端先删）不得在本次复活中恢复，即使它是第一类链接。
func TestEarlierInvalidatedLinkMustNotRestore(t *testing.T) {
	c, _ := newTestCoordinator(t)
	setupObjectWithLinks(t, c)

	// 先删除 b：L1 因对端删除在 t1 失效；然后 b 被复活（不恢复链接）。
	must(t, c.DeleteObject("b", "u", "p-del"))
	t1 := c.links["L1"].InvalidatedAt
	if t1 == 0 || c.links["L1"].Status != LinkInvalidated {
		t.Fatalf("L1 should be invalidated by b deletion")
	}
	must(t, c.ReviveObject("b", "u", "p-rev", t1, false, nil))
	// L1 仍处于失效状态，且失效时点是更早的 t1。

	// 再删除 a，L1 已是失效态，保留原失效时点不被覆盖。
	must(t, c.DeleteObject("a", "u", "p-del"))
	if c.links["L1"].InvalidatedAt != t1 {
		t.Fatalf("earlier invalidation timestamp must be preserved, got %d want %d", c.links["L1"].InvalidatedAt, t1)
	}
	t2 := c.objects["a"].lastDeleteAt

	// 请求恢复 L1：失效时点不等于本次删除时点 -> 整体失败。
	err := c.ReviveObject("a", "u", "p-rev", t2, true, []string{"L1"})
	assertErrIs(t, err, ErrLinkCondition)
	// 整体失败：对象仍删除、无新区间、链接不变。
	if c.objects["a"].alive {
		t.Fatal("object must remain deleted after failed revive")
	}
	if len(c.objects["a"].intervals) != 1 {
		t.Fatalf("no new interval allowed after failure, got %d", len(c.objects["a"].intervals))
	}
	if c.links["L1"].Status != LinkInvalidated || c.links["L1"].InvalidatedAt != t1 {
		t.Fatalf("L1 must remain unchanged after failure: %+v", c.links["L1"])
	}

	// 不恢复链接的复活可以成功，L1 依旧保持失效（不得偷偷恢复）。
	must(t, c.ReviveObject("a", "u", "p-rev", t2, false, nil))
	if c.links["L1"].Status != LinkInvalidated || c.links["L1"].InvalidatedAt != t1 {
		t.Fatalf("L1 must stay invalidated with restoreLinks=false")
	}
}

// 跳过中间循环指向更早的删除事件：可区分错误，且状态不变。
func TestStaleDeleteTargetRejected(t *testing.T) {
	c, _ := newTestCoordinator(t)
	setupObjectWithLinks(t, c)

	must(t, c.DeleteObject("a", "u", "p-del"))
	firstDelete := c.objects["a"].lastDeleteAt
	must(t, c.ReviveObject("a", "u", "p-rev", firstDelete, false, nil))
	must(t, c.Write("a", "u", "second-generation-write"))
	must(t, c.DeleteObject("a", "u", "p-del"))
	secondDelete := c.objects["a"].lastDeleteAt

	err := c.ReviveObject("a", "u", "p-rev", firstDelete, false, nil)
	assertErrIs(t, err, ErrStaleDeleteTarget)
	if len(c.objects["a"].intervals) != 2 {
		t.Fatalf("state must not change on stale target, intervals=%d", len(c.objects["a"].intervals))
	}
	// 指向最近一次删除则成功。
	must(t, c.ReviveObject("a", "u", "p-rev", secondDelete, false, nil))
}

// 拒绝优先级：权限 > 状态 > 指向过期 > 链接条件。
func TestRejectionPriority(t *testing.T) {
	c, _ := newTestCoordinator(t)
	setupObjectWithLinks(t, c)

	// 存活对象复活：状态不符优先于指向过期与链接条件。
	err := c.ReviveObject("a", "u", "p-rev", 12345, true, []string{"L1"})
	assertErrIs(t, err, ErrStateMismatch)

	// 存活对象复活且权限不足：权限优先于状态。
	err = c.ReviveObject("a", "u", "p-none", 12345, true, []string{"L1"})
	assertErrIs(t, err, ErrPermissionDenied)

	must(t, c.DeleteObject("a", "u", "p-del"))
	deleteAt := c.objects["a"].lastDeleteAt

	// 删除态复活但权限不足：权限优先于一切（包括链接条件）。
	err = c.ReviveObject("a", "u", "p-none", deleteAt, true, []string{"L2"})
	assertErrIs(t, err, ErrPermissionDenied)
	// 权限足够但指向过期：优先于链接条件（L2 是第二类、本不满足恢复条件）。
	err = c.ReviveObject("a", "u", "p-rev", deleteAt-1, true, []string{"L2"})
	assertErrIs(t, err, ErrStaleDeleteTarget)
	// 指向正确，链接条件失败：整次复活失败。
	err = c.ReviveObject("a", "u", "p-rev", deleteAt, true, []string{"L2"})
	assertErrIs(t, err, ErrLinkCondition)
	if c.objects["a"].alive {
		t.Fatal("object stays deleted after link-condition failure")
	}

	// 删除操作的权限不足同样最高优先（对已删除对象再删也先报权限）。
	err = c.DeleteObject("a", "u", "p-none")
	assertErrIs(t, err, ErrPermissionDenied)
}

// 权限吊销不影响历史审计快照可读性。
func TestRevokedPermissionKeepsAuditReadable(t *testing.T) {
	c, _ := newTestCoordinator(t)
	setupObjectWithLinks(t, c)
	must(t, c.DeleteObject("a", "u", "p-del"))
	must(t, c.UpsertPermission(&PermissionEntry{ID: "p-del", Grants: []string{ActionDelete}, Version: 2, Revoked: true}))

	rep, err := c.Audit("a")
	must(t, err)
	var del *Event
	for _, ev := range rep.Events {
		if ev.Kind == EventDelete {
			del = ev
		}
	}
	if del == nil || del.Permission == nil {
		t.Fatal("delete event with permission snapshot missing")
	}
	if del.Permission.PermissionID != "p-del" || del.Permission.WasRevoked {
		t.Fatalf("snapshot must capture pre-revocation state: %+v", del.Permission)
	}
	// 已吊销权限不能再授权新的删除。
	must(t, c.ReviveObject("a", "u", "p-rev", del.At, false, nil))
	err = c.DeleteObject("a", "u", "p-del")
	assertErrIs(t, err, ErrPermissionDenied)
}

// 删除态下常规读写报错，但审计查询始终可用。
func TestDeletedObjectBlocksIOButAuditAlwaysAvailable(t *testing.T) {
	c, _ := newTestCoordinator(t)
	setupObjectWithLinks(t, c)
	must(t, c.DeleteObject("a", "u", "p-del"))

	assertErrIs(t, c.Write("a", "u", "x"), ErrObjectDeleted)
	assertErrIs(t, c.Read("a", "u", "x"), ErrObjectDeleted)
	before := len(c.objects["a"].events)

	rep, err := c.Audit("a")
	must(t, err)
	if len(rep.Events) != before {
		t.Fatal("rejected IO must not append audit events")
	}
	if rep.Intervals[len(rep.Intervals)-1].EndedAt == 0 {
		t.Fatal("last interval should be closed")
	}
	// 审计返回深拷贝，外部修改不影响内部状态。
	rep.Events[0].Actor = "tampered"
	again, err := c.Audit("a")
	must(t, err)
	if again.Events[0].Actor == "tampered" {
		t.Fatal("Audit must return defensive copies")
	}
}

// 性能要求：链接判定的历史扫描计数恒为 0，且判定次数不随循环次数增长（每链接每次复活 O(1)）。
func TestLinkCheckIsConstantTimeRegardlessOfCycles(t *testing.T) {
	c, _ := newTestCoordinator(t)
	setupObjectWithLinks(t, c)

	checkCounts := []int64{}
	for i := 0; i < 8; i++ {
		must(t, c.DeleteObject("a", "u", "p-del"))
		delAt := c.objects["a"].lastDeleteAt
		before := c.Stats().LinkChecks
		must(t, c.ReviveObject("a", "u", "p-rev", delAt, true, []string{"L1", "L3"}))
		st := c.Stats()
		checkCounts = append(checkCounts, st.LinkChecks-before)
		if st.HistoryScansDuringLinkChecks != 0 {
			t.Fatalf("history scans must be zero, got %d", st.HistoryScansDuringLinkChecks)
		}
	}
	// 每一轮无论已经经历多少次循环，对 2 条链接的判定次数恒为 2。
	for i, n := range checkCounts {
		if n != 2 {
			t.Fatalf("cycle %d: want 2 O(1) checks, got %d", i, n)
		}
	}
}

// 判定日志必须打印每次判定的输入、输出与依据。
func TestDecisionLogging(t *testing.T) {
	c, _ := newTestCoordinator(t)
	setupObjectWithLinks(t, c)
	var raw bytes.Buffer
	c.logWriter = &raw

	must(t, c.DeleteObject("a", "u", "p-del"))
	delAt := c.objects["a"].lastDeleteAt
	err := c.ReviveObject("a", "u", "p-none", delAt, false, nil)
	assertErrIs(t, err, ErrPermissionDenied)

	logs := raw.String()
	for _, want := range []string{
		`"decision":"delete"`, `"op":"delete"`, `"object":"a"`, `"actor":"u"`,
		`"permission":"p-del"`, `"result":"allow"`,
		`"decision":"revive"`, `"deny_permission"`, `lacks action`,
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("log missing %q\nlogs:\n%s", want, logs)
		}
	}

	raw.Reset()
	err = c.ReviveObject("a", "u", "p-rev", delAt, true, []string{"L2"})
	assertErrIs(t, err, ErrLinkCondition)
	linkLog := raw.String()
	for _, want := range []string{
		`"decision":"revive_link_decision"`, `"link":"L2"`,
		`"independent_link_type"`, `"deny_link_condition"`,
		`"delete_at":`, `"invalidated_at":`,
	} {
		if !strings.Contains(linkLog, want) {
			t.Fatalf("link decision log missing %q\n%s", want, linkLog)
		}
	}
}

// 并发：大量删除/复活/读写/审计并发执行，结果必须等价于某个全局串行顺序。
func TestConcurrentSerializability(t *testing.T) {
	c, _ := newTestCoordinator(t)
	must(t, c.CreateObject("o", "creator"))

	const goroutines = 16
	const rounds = 200
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				rep, err := c.Audit("o")
				if err != nil {
					errs <- err
					return
				}
				// 任何前缀下：最后一个删除/复活后状态必须与区间开闭自洽，
				// 且每个事件都归属到存在的区间，区间互不重叠。
				if err := validateReportConsistency(rep); err != nil {
					errs <- err
					return
				}
				alive := rep.Intervals[len(rep.Intervals)-1].EndedAt == 0
				if alive {
					switch r % 3 {
					case 0:
						if err := c.Write("o", "u", "x"); err != nil && !errors.Is(err, ErrObjectDeleted) {
							errs <- err
							return
						}
					case 1:
						if err := c.DeleteObject("o", "u", "p-del"); err != nil && !errors.Is(err, ErrStateMismatch) {
							errs <- err
							return
						}
					}
				} else {
					lastDelete := int64(0)
					for _, ev := range rep.Events {
						if ev.Kind == EventDelete {
							lastDelete = ev.At
						}
					}
					if err := c.ReviveObject("o", "u", "p-rev", lastDelete, false, nil); err != nil &&
						!errors.Is(err, ErrStateMismatch) && !errors.Is(err, ErrStaleDeleteTarget) {
						errs <- err
						return
					}
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	rep, err := c.Audit("o")
	must(t, err)
	if err := validateReportConsistency(rep); err != nil {
		t.Fatal(err)
	}
	// 事件数与区间数必须与串行接受的操作一一对应（无重复、无遗漏）。
	var deletes, revives int
	for _, ev := range rep.Events {
		switch ev.Kind {
		case EventDelete:
			deletes++
		case EventRevive:
			revives++
		}
	}
	if deletes != revives && deletes != revives+1 {
		t.Fatalf("unbalanced delete/revive counts: %d vs %d", deletes, revives)
	}
}

func validateReportConsistency(rep *AuditReport) error {
	if len(rep.Intervals) == 0 {
		return errors.New("empty intervals")
	}
	ids := map[string]bool{}
	var prevStart, prevEnd int64
	for i, iv := range rep.Intervals {
		if ids[iv.ID] {
			return fmt.Errorf("duplicate interval %s", iv.ID)
		}
		ids[iv.ID] = true
		if i > 0 && iv.StartedAt <= prevStart {
			return fmt.Errorf("interval starts not strictly increasing")
		}
		if i > 0 && iv.StartedAt <= prevEnd {
			return fmt.Errorf("intervals overlap: %d <= %d", iv.StartedAt, prevEnd)
		}
		if iv.EndedAt != 0 && iv.EndedAt < iv.StartedAt {
			return fmt.Errorf("interval ends before start")
		}
		prevStart, prevEnd = iv.StartedAt, iv.EndedAt
	}
	for _, ev := range rep.Events {
		if !ids[ev.IntervalID] {
			return fmt.Errorf("event %d points to unknown interval %s", ev.Seq, ev.IntervalID)
		}
	}
	return nil
}
