package ontology

import (
	"errors"
	"testing"
)

func newTestLedger(t *testing.T, totalBlocks, orphanCapacity, linkLimit int) *Ledger {
	t.Helper()
	ledger, err := NewLedger(totalBlocks, orphanCapacity, linkLimit)
	if err != nil {
		t.Fatalf("NewLedger() error = %v", err)
	}
	return ledger
}

func requireErrorIs(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want error matching %v", got, want)
	}
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func requireInt(t *testing.T, name string, got, want int) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %d, want %d", name, got, want)
	}
}

func mustLinks(t *testing.T, ledger *Ledger, id int) int {
	t.Helper()
	links, err := ledger.Links(id)
	requireNoError(t, err)
	return links
}

func TestNewLedgerRejectsInvalidParameters(t *testing.T) {
	cases := []struct{ totalBlocks, capacity, linkLimit int }{
		{0, 1, 1},
		{1, 0, 1},
		{1, 1, 0},
	}
	for _, tc := range cases {
		_, err := NewLedger(tc.totalBlocks, tc.capacity, tc.linkLimit)
		requireErrorIs(t, err, ErrInvalidArgument)
	}
}

func TestUnlinkOpenEntersOrphanAndUnlinkedClosedReleases(t *testing.T) {
	ledger := newTestLedger(t, 100, 3, 5)
	openInode, err := ledger.Create(10)
	requireNoError(t, err)
	handle, err := ledger.Open(openInode)
	requireNoError(t, err)
	requireNoError(t, ledger.Unlink(openInode))

	if !ledger.Exists(openInode) {
		t.Fatal("open unlinked inode was deleted")
	}
	requireInt(t, "used while orphaned", ledger.Used(), 10)
	if ids := ledger.OrphanIDs(); len(ids) != 1 || ids[0] != openInode {
		t.Fatalf("orphans = %v, want [%d]", ids, openInode)
	}

	closedInode, err := ledger.Create(10)
	requireNoError(t, err)
	requireNoError(t, ledger.Unlink(closedInode))
	if ledger.Exists(closedInode) {
		t.Fatal("closed unlinked inode still exists")
	}
	requireInt(t, "used after immediate release", ledger.Used(), 10)

	requireNoError(t, ledger.Close(handle))
	if ledger.Exists(openInode) {
		t.Fatal("orphan was not released by final close")
	}
	requireInt(t, "used after final close", ledger.Used(), 0)
	if ids := ledger.OrphanIDs(); len(ids) != 0 {
		t.Fatalf("orphans = %v, want []", ids)
	}
}

func TestCrashReclaimsOrphansInReverseInsertionOrder(t *testing.T) {
	ledger := newTestLedger(t, 100, 3, 5)
	for i := 0; i < 3; i++ {
		id, err := ledger.Create(10)
		requireNoError(t, err)
		_, err = ledger.Open(id)
		requireNoError(t, err)
		requireNoError(t, ledger.Unlink(id))
	}

	if ids := ledger.OrphanIDs(); len(ids) != 3 || ids[0] != 3 || ids[1] != 2 || ids[2] != 1 {
		t.Fatalf("orphans = %v, want [3 2 1]", ids)
	}
	results := ledger.Crash()
	want := []CrashResult{{3, CrashDelete}, {2, CrashDelete}, {1, CrashDelete}}
	for i := range want {
		if results[i] != want[i] {
			t.Fatalf("crash result %d = %+v, want %+v", i, results[i], want[i])
		}
	}
}

func TestCloseMiddleOrphanPreservesRelativeOrder(t *testing.T) {
	ledger := newTestLedger(t, 100, 3, 5)
	handles := make([]int, 3)
	for i := 0; i < 3; i++ {
		id, err := ledger.Create(10)
		requireNoError(t, err)
		handles[i], err = ledger.Open(id)
		requireNoError(t, err)
		requireNoError(t, ledger.Unlink(id))
	}
	requireNoError(t, ledger.Close(handles[1]))
	if ids := ledger.OrphanIDs(); len(ids) != 2 || ids[0] != 3 || ids[1] != 1 {
		t.Fatalf("orphans = %v, want [3 1]", ids)
	}
	results := ledger.Crash()
	if len(results) != 2 || results[0].Inode != 3 || results[1].Inode != 1 {
		t.Fatalf("crash results = %+v, want inodes [3 1]", results)
	}
}

func TestMultipleHandlesReleaseOnlyOnFinalClose(t *testing.T) {
	ledger := newTestLedger(t, 100, 3, 5)
	id, err := ledger.Create(7)
	requireNoError(t, err)
	first, err := ledger.Open(id)
	requireNoError(t, err)
	second, err := ledger.Open(id)
	requireNoError(t, err)
	requireNoError(t, ledger.Unlink(id))
	requireNoError(t, ledger.Close(first))
	if !ledger.Exists(id) {
		t.Fatal("inode released before final handle close")
	}
	requireInt(t, "used after first close", ledger.Used(), 7)
	requireNoError(t, ledger.Close(second))
	if ledger.Exists(id) {
		t.Fatal("inode exists after final handle close")
	}
	requireInt(t, "used after final close", ledger.Used(), 0)
}

func TestShrinkOnOrphanReturnsBlocks(t *testing.T) {
	ledger := newTestLedger(t, 100, 3, 5)
	id, err := ledger.Create(10)
	requireNoError(t, err)
	_, err = ledger.Open(id)
	requireNoError(t, err)
	requireNoError(t, ledger.Unlink(id))
	requireNoError(t, ledger.Shrink(id, 3))
	blocks, err := ledger.Blocks(id)
	requireNoError(t, err)
	requireInt(t, "orphan blocks", blocks, 3)
	requireInt(t, "used blocks", ledger.Used(), 3)
}

func TestLinkAndOpenRejectedOnLinklessOrphan(t *testing.T) {
	ledger := newTestLedger(t, 100, 3, 5)
	id, err := ledger.Create(10)
	requireNoError(t, err)
	_, err = ledger.Open(id)
	requireNoError(t, err)
	requireNoError(t, ledger.Unlink(id))
	requireErrorIs(t, ledger.Link(id), ErrNoLinks)
	_, err = ledger.Open(id)
	requireErrorIs(t, err, ErrNoLinks)
	opens, err := ledger.OpenCount(id)
	requireNoError(t, err)
	requireInt(t, "opens", opens, 1)
}

func TestFullOrphanListRejectsNewMembersButAllowsExistingMember(t *testing.T) {
	ledger := newTestLedger(t, 200, 1, 5)
	first, err := ledger.Create(10)
	requireNoError(t, err)
	firstHandle, err := ledger.Open(first)
	requireNoError(t, err)
	requireNoError(t, ledger.Unlink(first))
	second, err := ledger.Create(10)
	requireNoError(t, err)
	secondHandle, err := ledger.Open(second)
	requireNoError(t, err)
	third, err := ledger.Create(10)
	requireNoError(t, err)
	requireErrorIs(t, ledger.Unlink(second), ErrOrphanListFull)
	requireErrorIs(t, ledger.BeginShrink(third, 1), ErrOrphanListFull)
	requireNoError(t, ledger.BeginShrink(first, 2))
	requireErrorIs(t, ledger.BeginShrink(first, 3), ErrShrinkInProgress)
	requireInt(t, "used after rejections", ledger.Used(), 30)
	if ids := ledger.OrphanIDs(); len(ids) != 1 || ids[0] != first {
		t.Fatalf("orphans = %v, want [%d]", ids, first)
	}
	owner, err := ledger.HandleInode(secondHandle)
	requireNoError(t, err)
	requireInt(t, "rejected handle owner", owner, second)
	firstOwner, err := ledger.HandleInode(firstHandle)
	requireNoError(t, err)
	requireInt(t, "first handle owner", firstOwner, first)
	nextHandle, err := ledger.Open(third)
	requireNoError(t, err)
	requireInt(t, "next handle after rejected operations", nextHandle, secondHandle+1)
}

func TestDualConditionKeepsPositionAndFinishKeepsUnlinkedOpenInode(t *testing.T) {
	ledger := newTestLedger(t, 100, 3, 5)
	first, err := ledger.Create(10)
	requireNoError(t, err)
	second, err := ledger.Create(10)
	requireNoError(t, err)
	requireNoError(t, ledger.BeginShrink(first, 4))
	_, err = ledger.Open(first)
	requireNoError(t, err)
	requireNoError(t, ledger.Unlink(first))
	_, err = ledger.Open(second)
	requireNoError(t, err)
	requireNoError(t, ledger.Unlink(second))
	if ids := ledger.OrphanIDs(); len(ids) != 2 || ids[0] != 2 || ids[1] != 1 {
		t.Fatalf("orphans = %v, want [2 1]", ids)
	}
	requireNoError(t, ledger.FinishShrink(first))
	if ids := ledger.OrphanIDs(); len(ids) != 2 || ids[0] != 2 || ids[1] != 1 {
		t.Fatalf("orphans after finish = %v, want [2 1]", ids)
	}
	blocks, err := ledger.Blocks(first)
	requireNoError(t, err)
	requireInt(t, "finished blocks", blocks, 4)
}

func TestBeginShrinkThenImmediateUnlinkDeletesPendingRegistration(t *testing.T) {
	ledger := newTestLedger(t, 100, 3, 5)
	id, err := ledger.Create(10)
	requireNoError(t, err)
	requireNoError(t, ledger.BeginShrink(id, 2))
	requireNoError(t, ledger.Unlink(id))
	if ledger.Exists(id) {
		t.Fatal("pending inode with no open handles was not deleted")
	}
	if ids := ledger.OrphanIDs(); len(ids) != 0 {
		t.Fatalf("orphans = %v, want []", ids)
	}
	requireInt(t, "used blocks", ledger.Used(), 0)
	requireErrorIs(t, ledger.FinishShrink(id), ErrNotFound)
}

func TestCrashTruncatesLinkedPendingOrphanAndInvalidatesHandle(t *testing.T) {
	ledger := newTestLedger(t, 100, 3, 5)
	id, err := ledger.Create(10)
	requireNoError(t, err)
	handle, err := ledger.Open(id)
	requireNoError(t, err)
	requireNoError(t, ledger.BeginShrink(id, 4))
	results := ledger.Crash()
	if len(results) != 1 || results[0] != (CrashResult{id, CrashTruncate}) {
		t.Fatalf("crash results = %+v, want one truncate for %d", results, id)
	}
	blocks, err := ledger.Blocks(id)
	requireNoError(t, err)
	requireInt(t, "blocks", blocks, 4)
	requireInt(t, "used", ledger.Used(), 4)
	_, pending, err := ledger.PendingShrink(id)
	requireNoError(t, err)
	if pending {
		t.Fatal("pending shrink survived crash")
	}
	requireErrorIs(t, ledger.Close(handle), ErrNotFound)
}

func TestLinkLimitAllowsExactlyL(t *testing.T) {
	ledger := newTestLedger(t, 100, 3, 3)
	id, err := ledger.Create(1)
	requireNoError(t, err)
	requireNoError(t, ledger.Link(id))
	requireNoError(t, ledger.Link(id))
	requireInt(t, "links", mustLinks(t, ledger, id), 3)
	requireErrorIs(t, ledger.Link(id), ErrLinksFull)
	requireInt(t, "links after rejection", mustLinks(t, ledger, id), 3)
}

func TestShrinkErrorPrecedence(t *testing.T) {
	ledger := newTestLedger(t, 100, 3, 5)
	id, err := ledger.Create(5)
	requireNoError(t, err)
	requireNoError(t, ledger.BeginShrink(id, 1))
	requireErrorIs(t, ledger.Shrink(id, -1), ErrInvalidArgument)
	requireErrorIs(t, ledger.Shrink(999, 0), ErrNotFound)
	requireErrorIs(t, ledger.Shrink(id, 6), ErrInvalidArgument)
	requireErrorIs(t, ledger.Shrink(id, 0), ErrShrinkInProgress)
	requireErrorIs(t, ledger.FinishShrink(999), ErrNotFound)
	requireNoError(t, ledger.FinishShrink(id))
}

func TestDocumentationExample(t *testing.T) {
	ledger := newTestLedger(t, 100, 3, 5)
	for range 3 {
		_, err := ledger.Create(10)
		requireNoError(t, err)
	}
	requireNoError(t, ledger.BeginShrink(1, 4))
	handle, err := ledger.Open(2)
	requireNoError(t, err)
	requireNoError(t, ledger.Unlink(2))
	requireNoError(t, ledger.BeginShrink(2, 2))
	requireNoError(t, ledger.Unlink(3))
	requireInt(t, "used before crash", ledger.Used(), 20)

	results := ledger.Crash()
	want := []CrashResult{{2, CrashDelete}, {1, CrashTruncate}}
	if len(results) != len(want) {
		t.Fatalf("crash results = %+v, want %+v", results, want)
	}
	for i := range want {
		if results[i] != want[i] {
			t.Fatalf("crash result %d = %+v, want %+v", i, results[i], want[i])
		}
	}
	requireInt(t, "used after crash", ledger.Used(), 4)
	requireErrorIs(t, ledger.Close(handle), ErrNotFound)
}

func TestDocumentationFinishBranch(t *testing.T) {
	ledger := newTestLedger(t, 100, 3, 5)
	for range 3 {
		_, err := ledger.Create(10)
		requireNoError(t, err)
	}
	requireNoError(t, ledger.BeginShrink(1, 4))
	handle, err := ledger.Open(2)
	requireNoError(t, err)
	requireNoError(t, ledger.Unlink(2))
	requireNoError(t, ledger.BeginShrink(2, 2))
	requireNoError(t, ledger.Unlink(3))
	requireNoError(t, ledger.FinishShrink(1))
	requireInt(t, "used after finish 1", ledger.Used(), 14)
	requireNoError(t, ledger.FinishShrink(2))
	requireInt(t, "used after finish 2", ledger.Used(), 6)
	requireNoError(t, ledger.Close(handle))
	requireInt(t, "used after close", ledger.Used(), 4)
}
