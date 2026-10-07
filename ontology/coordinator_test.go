package ontology

import (
	"errors"
	"fmt"
	"testing"
)

// newFixture 构造一个带两类链接类型与删除/复活权限条目的协调器。
func newFixture() *Coordinator {
	c := New()
	c.RegisterLinkType(LinkType{Name: "dep", Policy: CascadeInvalidate})
	c.RegisterLinkType(LinkType{Name: "tag", Policy: Independent})
	c.AddGrant(Grant{ID: "g-del", Actor: "alice", Action: ActionDelete})
	c.AddGrant(Grant{ID: "g-rev", Actor: "alice", Action: ActionRevive})
	return c
}

func mustCreate(t *testing.T, c *Coordinator, ids ...ObjectID) {
	t.Helper()
	for _, id := range ids {
		if err := c.CreateObject("alice", id); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
}

func mustDelete(t *testing.T, c *Coordinator, id ObjectID) EventID {
	t.Helper()
	if err := c.DeleteObject("alice", id, "g-del"); err != nil {
		t.Fatalf("delete %s: %v", id, err)
	}
	h, err := c.History(id)
	if err != nil {
		t.Fatalf("history %s: %v", id, err)
	}
	return h.Intervals[len(h.Intervals)-1].ClosedBy
}

func mustRevive(t *testing.T, c *Coordinator, id ObjectID, resumes EventID, restore bool) {
	t.Helper()
	err := c.ReviveObject(ReviveRequest{
		Actor: "alice", Object: id, GrantID: "g-rev",
		ResumesDeletion: resumes, RestoreLinks: restore,
	})
	if err != nil {
		t.Fatalf("revive %s: %v", id, err)
	}
}

// validateChain 校验一段对象历史的溯源链结构不变量，
// 返回各次删除事件；结构被破坏时返回错误。
func validateChain(h ObjectHistory) ([]EventID, error) {
	fail := func(format string, args ...any) ([]EventID, error) {
		return nil, fmt.Errorf(format, args...)
	}
	if len(h.Intervals) == 0 {
		return fail("no intervals")
	}
	seen := map[IntervalID]bool{}
	var deletions []EventID
	var prevEnd LogicalTime
	for i, iv := range h.Intervals {
		if seen[iv.ID] {
			return fail("duplicate interval id %s", iv.ID)
		}
		seen[iv.ID] = true
		if iv.Index != i+1 {
			return fail("interval %s index %d, want %d", iv.ID, iv.Index, i+1)
		}
		if len(iv.Events) == 0 {
			return fail("interval %s has no events", iv.ID)
		}
		// 区间内事件时间严格递增，且全部归属本区间。
		for j, e := range iv.Events {
			if e.Interval != iv.ID {
				return fail("event %s attributed to %s, found in %s", e.ID, e.Interval, iv.ID)
			}
			if j > 0 && e.Time <= iv.Events[j-1].Time {
				return fail("interval %s events not ordered", iv.ID)
			}
		}
		// 区间边界确定且互不重叠：本区间全部事件晚于上一区间的结束。
		if first := iv.Events[0].Time; i > 0 && first <= prevEnd {
			return fail("interval %s overlaps previous interval", iv.ID)
		}
		last := iv.Events[len(iv.Events)-1]
		if i < len(h.Intervals)-1 {
			// 非末尾区间必须被删除事件关闭。
			if iv.ClosedBy == "" || last.Kind != EventDelete || last.ID != iv.ClosedBy {
				return fail("interval %s not closed by a delete event", iv.ID)
			}
			deletions = append(deletions, iv.ClosedBy)
			prevEnd = last.Time
		} else if iv.Open() {
			if !h.Alive {
				return fail("last interval open but object not alive")
			}
		} else {
			if h.Alive {
				return fail("last interval closed but object alive")
			}
			deletions = append(deletions, iv.ClosedBy)
		}
		// 复活事件必须精确指向前一区间的关闭删除事件。
		if i > 0 {
			first := iv.Events[0]
			if first.Kind != EventRevive || first.ResumesDeletion != h.Intervals[i-1].ClosedBy {
				return fail("interval %s opens with bad resume pointer", iv.ID)
			}
		}
	}
	return deletions, nil
}

// checkChain 是 validateChain 的测试便捷封装，返回各次删除事件。
func checkChain(t *testing.T, h ObjectHistory) []EventID {
	t.Helper()
	deletions, err := validateChain(h)
	if err != nil {
		t.Fatalf("broken provenance chain: %v", err)
	}
	return deletions
}

// TestIntervalAttribution 多次删除复活循环下溯源链的唯一归属。
func TestIntervalAttribution(t *testing.T) {
	c := newFixture()
	mustCreate(t, c, "o1")
	if err := c.WriteObject("alice", "o1", "v1"); err != nil {
		t.Fatal(err)
	}
	d1 := mustDelete(t, c, "o1")
	mustRevive(t, c, "o1", d1, false)
	if err := c.ReadObject("alice", "o1"); err != nil {
		t.Fatal(err)
	}
	d2 := mustDelete(t, c, "o1")
	mustRevive(t, c, "o1", d2, false)
	d3 := mustDelete(t, c, "o1")
	mustRevive(t, c, "o1", d3, false)

	h, err := c.History("o1")
	if err != nil {
		t.Fatal(err)
	}
	deletions := checkChain(t, h)
	if len(h.Intervals) != 4 {
		t.Fatalf("got %d intervals, want 4", len(h.Intervals))
	}
	if len(deletions) != 3 || deletions[0] != d1 || deletions[1] != d2 || deletions[2] != d3 {
		t.Fatalf("deletion chain mismatch: %v", deletions)
	}
	// 读写事件归属正确的区间。
	if got := h.Intervals[0].Events[1].Kind; got != EventWrite {
		t.Fatalf("write event kind = %v", got)
	}
	if got := h.Intervals[1].Events[1].Kind; got != EventRead {
		t.Fatalf("read event kind = %v", got)
	}
}

// TestLinkPolicies 两类链接在对象删除时的行为差异：
// 级联失效类带上失效时点进入不可用状态，独立类不受影响。
func TestLinkPolicies(t *testing.T) {
	c := newFixture()
	mustCreate(t, c, "a", "b")
	if err := c.LinkObjects("alice", "l-dep", "dep", "a", "b"); err != nil {
		t.Fatal(err)
	}
	if err := c.LinkObjects("alice", "l-tag", "tag", "a", "b"); err != nil {
		t.Fatal(err)
	}
	mustDelete(t, c, "a")

	dep, err := c.GetLink("l-dep")
	if err != nil {
		t.Fatal(err)
	}
	if dep.Available() || dep.InvalidatedAt == 0 {
		t.Fatalf("cascade link should be invalidated, got %+v", dep)
	}
	tag, err := c.GetLink("l-tag")
	if err != nil {
		t.Fatal(err)
	}
	if !tag.Available() {
		t.Fatalf("independent link should stay available, got %+v", tag)
	}
}

// TestReviveRestoresOnlyMatchingLinks 复活只恢复失效时点恰好等于
// 本次删除时点的级联链接；因另一端对象先被删除而更早失效的链接不得恢复。
func TestReviveRestoresOnlyMatchingLinks(t *testing.T) {
	c := newFixture()
	mustCreate(t, c, "a", "b", "c")
	if err := c.LinkObjects("alice", "l1", "dep", "a", "b"); err != nil {
		t.Fatal(err)
	}
	if err := c.LinkObjects("alice", "l2", "dep", "a", "c"); err != nil {
		t.Fatal(err)
	}
	// b 先被删除：l1 因另一端 b 的删除而失效，早于 a 自身的删除。
	mustDelete(t, c, "b")
	da := mustDelete(t, c, "a")

	l1, _ := c.GetLink("l1")
	l2, _ := c.GetLink("l2")
	if l1.InvalidatedAt == 0 || l1.InvalidatedAt == l2.InvalidatedAt {
		t.Fatalf("setup broken: l1=%+v l2=%+v", l1, l2)
	}
	// 复活 a：l2 的失效时点等于 a 的删除时点，应恢复；
	// l1 因 b 先被删除而更早失效，不得恢复。
	mustRevive(t, c, "a", da, true)
	l1, _ = c.GetLink("l1")
	l2, _ = c.GetLink("l2")
	if !l2.Available() {
		t.Fatalf("l2 should be restored, got %+v", l2)
	}
	if l1.Available() {
		t.Fatalf("l1 must not be restored, got %+v", l1)
	}
}

// TestReviveExplicitLinkConditionFails 显式请求恢复不满足条件的链接时，
// 整次复活失败：对象保持删除状态、区间划分与链接记录均不变。
func TestReviveExplicitLinkConditionFails(t *testing.T) {
	c := newFixture()
	mustCreate(t, c, "a", "b", "c")
	if err := c.LinkObjects("alice", "l1", "dep", "a", "b"); err != nil {
		t.Fatal(err)
	}
	if err := c.LinkObjects("alice", "l2", "dep", "a", "c"); err != nil {
		t.Fatal(err)
	}
	// l1 因 b 先删除而失效；l2 将随 a 的删除失效。
	mustDelete(t, c, "b")
	da := mustDelete(t, c, "a")

	before, err := c.History("a")
	if err != nil {
		t.Fatal(err)
	}
	err = c.ReviveObject(ReviveRequest{
		Actor: "alice", Object: "a", GrantID: "g-rev",
		ResumesDeletion: da, RestoreLinks: true,
		LinkIDs: []LinkID{"l2", "l1"}, // l1 不满足条件
	})
	if !errors.Is(err, ErrLinkRestore) {
		t.Fatalf("want ErrLinkRestore, got %v", err)
	}
	after, err := c.History("a")
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Intervals) != len(before.Intervals) || after.Alive {
		t.Fatalf("rejected revive mutated state: %+v", after)
	}
	l2, _ := c.GetLink("l2")
	if l2.Available() {
		t.Fatalf("rejected revive partially restored l2")
	}
}

// TestStaleDeletionPointer 复活跳过中间的删除复活循环、
// 指向更早的删除事件时，报可区分的 ErrStaleDeletion。
func TestStaleDeletionPointer(t *testing.T) {
	c := newFixture()
	mustCreate(t, c, "o1")
	d1 := mustDelete(t, c, "o1")
	mustRevive(t, c, "o1", d1, false)
	d2 := mustDelete(t, c, "o1")

	err := c.ReviveObject(ReviveRequest{
		Actor: "alice", Object: "o1", GrantID: "g-rev",
		ResumesDeletion: d1, // 指向更早的删除事件
	})
	if !errors.Is(err, ErrStaleDeletion) {
		t.Fatalf("want ErrStaleDeletion, got %v", err)
	}
	if errors.Is(err, ErrObjectNotDeleted) || errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("error categories confused: %v", err)
	}
	// 指向不存在的删除事件同样拒绝。
	err = c.ReviveObject(ReviveRequest{
		Actor: "alice", Object: "o1", GrantID: "g-rev",
		ResumesDeletion: "evt-9999",
	})
	if !errors.Is(err, ErrStaleDeletion) {
		t.Fatalf("want ErrStaleDeletion, got %v", err)
	}
	// 正确指向 d2 则成功。
	mustRevive(t, c, "o1", d2, false)
}

// TestDeletedObjectAccess 已删除对象的常规读写在删除状态下报
// ErrObjectDeleted，但审计查询始终可用。
func TestDeletedObjectAccess(t *testing.T) {
	c := newFixture()
	mustCreate(t, c, "o1")
	mustDelete(t, c, "o1")

	if err := c.ReadObject("alice", "o1"); !errors.Is(err, ErrObjectDeleted) {
		t.Fatalf("read: want ErrObjectDeleted, got %v", err)
	}
	if err := c.WriteObject("alice", "o1", "x"); !errors.Is(err, ErrObjectDeleted) {
		t.Fatalf("write: want ErrObjectDeleted, got %v", err)
	}
	h, err := c.History("o1")
	if err != nil {
		t.Fatalf("audit query must work on deleted object: %v", err)
	}
	if h.Alive || len(h.Intervals) != 1 {
		t.Fatalf("bad history: %+v", h)
	}
}

// TestRejectionPriority 权限不足优先于状态不符，状态不符优先于
// 链接恢复条件不满足。
func TestRejectionPriority(t *testing.T) {
	c := newFixture()
	mustCreate(t, c, "a", "b")
	if err := c.LinkObjects("alice", "l1", "dep", "a", "b"); err != nil {
		t.Fatal(err)
	}
	c.AddGrant(Grant{ID: "g-rev-b", Actor: "bob", Action: ActionRevive})

	// 权限不足 + 状态不符同时存在：报权限不足。
	err := c.ReviveObject(ReviveRequest{
		Actor: "alice", Object: "a", GrantID: "g-rev-b", // bob 的条目
		ResumesDeletion: "evt-1",
	})
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("want ErrPermissionDenied, got %v", err)
	}
	// 状态不符（对象存活）+ 指向错误的删除事件：报状态不符。
	err = c.ReviveObject(ReviveRequest{
		Actor: "alice", Object: "a", GrantID: "g-rev",
		ResumesDeletion: "evt-1",
	})
	if !errors.Is(err, ErrObjectNotDeleted) {
		t.Fatalf("want ErrObjectNotDeleted, got %v", err)
	}
	// 状态不符（重复删除）：报 ErrObjectDeleted。
	mustDelete(t, c, "a")
	if err := c.DeleteObject("alice", "a", "g-del"); !errors.Is(err, ErrObjectDeleted) {
		t.Fatalf("want ErrObjectDeleted, got %v", err)
	}
	// 权限不足（删除）优先于状态不符。
	if err := c.DeleteObject("alice", "a", "g-rev"); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("want ErrPermissionDenied, got %v", err)
	}
}

// TestGrantRevocationKeepsAuditReadable 权限条目被吊销后，
// 历史审计记录中的条目快照仍完整可读。
func TestGrantRevocationKeepsAuditReadable(t *testing.T) {
	c := newFixture()
	mustCreate(t, c, "o1")
	d1 := mustDelete(t, c, "o1")
	c.RevokeGrant("g-del")
	c.RevokeGrant("g-rev")

	h, err := c.History("o1")
	if err != nil {
		t.Fatal(err)
	}
	var del Event
	for _, e := range h.Intervals[0].Events {
		if e.ID == d1 {
			del = e
		}
	}
	if del.Grant.ID != "g-del" || del.Grant.Actor != "alice" || del.Grant.Action != ActionDelete {
		t.Fatalf("grant snapshot unreadable after revocation: %+v", del.Grant)
	}
	if del.Grant.Revoked {
		t.Fatalf("snapshot must reflect grant state at operation time")
	}
	// 吊销后复活因权限不足被拒绝。
	err = c.ReviveObject(ReviveRequest{
		Actor: "alice", Object: "o1", GrantID: "g-rev", ResumesDeletion: d1,
	})
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("want ErrPermissionDenied, got %v", err)
	}
}

// TestReviveEventDeclaresRestore 复活事件必须声明是否尝试恢复链接，
// 并记录实际恢复的链接集合。
func TestReviveEventDeclaresRestore(t *testing.T) {
	c := newFixture()
	mustCreate(t, c, "a", "b")
	if err := c.LinkObjects("alice", "l1", "dep", "a", "b"); err != nil {
		t.Fatal(err)
	}
	d1 := mustDelete(t, c, "a")
	mustRevive(t, c, "a", d1, true)

	h, err := c.History("a")
	if err != nil {
		t.Fatal(err)
	}
	rev := h.Intervals[1].Events[0]
	if rev.Kind != EventRevive || !rev.RestoreLinks {
		t.Fatalf("revive event must declare restore intent: %+v", rev)
	}
	if len(rev.RestoredLinks) != 1 || rev.RestoredLinks[0] != "l1" {
		t.Fatalf("restored links = %v, want [l1]", rev.RestoredLinks)
	}
	if rev.Grant.ID != "g-rev" || rev.Actor != "alice" || rev.Time == 0 {
		t.Fatalf("revive event missing actor/time/grant: %+v", rev)
	}
}
