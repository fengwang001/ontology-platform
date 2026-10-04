package edge_test

import (
	"errors"
	"testing"

	"ontology/edge"
)

func TestSpecPrewarmExample(t *testing.T) {
	cp, reg, _ := newCP(t, 1000, ql("t", 3, 1, 3))

	// 先制造纪元 2 的规则，使 /img/a.jpg 不新鲜
	if _, _, err := cp.Purge(1, "t", []string{"/img/", "/css/x.css"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := cp.Purge(1, "t", []string{"/img/a.jpg", "/img/b.jpg"}); err != nil {
		t.Fatal(err)
	}

	w, err := cp.Prewarm(1, "t", []string{"/img/a.jpg", "/v/1.ts", "/v/1.ts"})
	if err != nil || w != 2 {
		t.Fatalf("prewarm1 w=%d err=%v", w, err)
	}
	w, err = cp.Prewarm(1, "t", []string{"/v/1.ts", "/v/2.ts"})
	if err != nil || w != 1 {
		t.Fatalf("prewarm2 w=%d err=%v", w, err)
	}
	_, _, usedW, _ := reg.Used("t", 1)
	if usedW != 3 {
		t.Fatalf("used warm = %d want 3", usedW)
	}

	// 恰等后再来一个：配额不足，且 /v/2.ts 仍在队列
	if _, err := cp.Prewarm(1, "t", []string{"/v/3.ts"}); !errors.Is(err, edge.ErrWarmQuota) {
		t.Fatalf("warm quota: %v", err)
	}

	// 其间 1.ts 已新鲜
	if err := cp.Fill(1, "t", "/v/1.ts"); err != nil {
		t.Fatal(err)
	}
	lists, err := cp.Tick(1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(lists.Filled) != 1 || lists.Filled[0] != "/img/a.jpg" {
		t.Fatalf("filled = %v", lists.Filled)
	}
	if len(lists.Skipped) != 1 || lists.Skipped[0] != "/v/1.ts" {
		t.Fatalf("skipped = %v", lists.Skipped)
	}

	// 跳过不退配额
	_, _, usedW, _ = reg.Used("t", 1)
	if usedW != 3 {
		t.Fatalf("used warm after tick = %d", usedW)
	}

	// 2.ts 留在队列
	again, _ := cp.Tick(1, 10)
	if len(again.Filled) != 1 || again.Filled[0] != "/v/2.ts" {
		t.Fatalf("tail = %v", again)
	}

	// 预热执行后条目新鲜
	st, _ := cp.Fresh(1, "t", "/img/a.jpg")
	if st != edge.StatusFresh {
		t.Fatalf("prewarmed entry = %v", st)
	}
}

func TestPurgeDoesNotRemoveQueue(t *testing.T) {
	cp, _, _ := newCP(t, 1000, ql("t", 10, 10, 10))
	if _, err := cp.Prewarm(1, "t", []string{"/a"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := cp.Purge(1, "t", []string{"/"}); err != nil {
		t.Fatal(err)
	}
	lists, err := cp.Tick(1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(lists.Filled) != 1 || lists.Filled[0] != "/a" {
		t.Fatalf("queue lost after purge: %v", lists)
	}
}

func TestFreshMissingVsStale(t *testing.T) {
	cp, _, _ := newCP(t, 1000, ql("t", 10, 10, 10))
	st, _ := cp.Fresh(1, "t", "/never")
	if st != edge.StatusMissing {
		t.Fatalf("missing = %v", st)
	}
	if err := cp.Fill(1, "t", "/x"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := cp.Purge(1, "t", []string{"/x"}); err != nil {
		t.Fatal(err)
	}
	st, _ = cp.Fresh(1, "t", "/x")
	if st != edge.StatusStale {
		t.Fatalf("stale = %v", st)
	}
}
