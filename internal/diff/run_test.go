package diff_test

import (
	"bufio"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"ontology/internal/naive"
	"ontology/internal/whiteboard"
)

const diffSequences = 1500

func wbSnapToCanon(s whiteboard.Snapshot) canonicalSnap {
	c := canonicalSnap{
		order:  strings.Join(s.Order, "|"),
		rev:    s.Rev,
		lastTs: s.LastTs,
		elems:  map[string][2]string{},
		groups: map[string]string{},
		locks:  map[string][2]string{},
	}
	for id, e := range s.Elems {
		c.elems[id] = [2]string{e.Group, fmt.Sprint(e.LastRev)}
	}
	for gid, members := range s.Groups {
		c.groups[gid] = strings.Join(members, "|")
	}
	for id, l := range s.Locks {
		c.locks[id] = [2]string{l.Holder, fmt.Sprint(l.ExpireAt)}
	}
	return c
}

func nbSnapToCanon(s naive.Snapshot) canonicalSnap {
	c := canonicalSnap{
		order:  strings.Join(s.Order, "|"),
		rev:    s.Rev,
		lastTs: s.LastTs,
		elems:  map[string][2]string{},
		groups: map[string]string{},
		locks:  map[string][2]string{},
	}
	for id, e := range s.Elems {
		c.elems[id] = [2]string{e.Group, fmt.Sprint(e.LastRev)}
	}
	for gid, members := range s.Groups {
		c.groups[gid] = strings.Join(members, "|")
	}
	for id, l := range s.Locks {
		c.locks[id] = [2]string{l.Holder, fmt.Sprint(l.ExpireAt)}
	}
	return c
}

type canonicalSnap struct {
	order  string
	rev    int64
	lastTs int64
	elems  map[string][2]string
	groups map[string]string
	locks  map[string][2]string
}

func (c canonicalSnap) diff(d canonicalSnap) string {
	switch {
	case c.order != d.order:
		return fmt.Sprintf("order %q != %q", c.order, d.order)
	case c.rev != d.rev:
		return fmt.Sprintf("rev %d != %d", c.rev, d.rev)
	case c.lastTs != d.lastTs:
		return fmt.Sprintf("lastTs %d != %d", c.lastTs, d.lastTs)
	}
	if !reflect.DeepEqual(c.elems, d.elems) {
		return fmt.Sprintf("elems %#v != %#v", c.elems, d.elems)
	}
	if !reflect.DeepEqual(c.groups, d.groups) {
		return fmt.Sprintf("groups %#v != %#v", c.groups, d.groups)
	}
	if !reflect.DeepEqual(c.locks, d.locks) {
		return fmt.Sprintf("locks %#v != %#v", c.locks, d.locks)
	}
	return ""
}

func formatOp(o op) string {
	names := []string{"Add", "Group", "Ungroup", "Remove", "Reorder", "Lock", "Unlock", "Order", "Rank", "Between", "Rev"}
	s := fmt.Sprintf("%s(user=%q now=%d", names[o.kind], o.user, o.now)
	switch o.kind {
	case opGroup:
		s += fmt.Sprintf(" groupID=%q ids=%v)", o.id, o.ids)
	case opReorder:
		s += fmt.Sprintf(" target=%q anchor=%q side=%d baseRev=%d)", o.id, o.id2, o.side, o.baseRev)
	case opLock:
		s += fmt.Sprintf(" id=%q ttl=%d)", o.id, o.ttl)
	case opRank, opAdd, opUngroup, opRemove, opUnlock:
		s += fmt.Sprintf(" id=%q)", o.id)
	case opBetween:
		s += fmt.Sprintf(" lo=%d hi=%d)", o.lo, o.hi)
	default:
		s += ")"
	}
	return s
}

func formatOutcome(x outcome) string {
	if x.errKind == 0 {
		switch {
		case x.order != nil:
			return "ok -> " + strings.Join(x.order, ",")
		case x.rank != 0:
			return fmt.Sprintf("ok -> rank=%d", x.rank)
		case x.rev != 0:
			return fmt.Sprintf("ok -> rev=%d", x.rev)
		default:
			return "ok"
		}
	}
	if x.errKind == int(whiteboard.KindLocked) {
		return fmt.Sprintf("REJECT locked(holder=%q remain=%d target=%q)", x.holder, x.remain, x.target)
	}
	return fmt.Sprintf("REJECT kind=%d", x.errKind)
}

// runOneSequence 跑一条随机序列；logw 非 nil 时把输入/输出/判定写入日志。
func runOneSequence(t *testing.T, seed uint64, seqLen int, logw *bufio.Writer) {
	t.Helper()
	wb := whiteboard.New()
	nb := naive.New()
	g := &generator{rng: rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))}
	if logw != nil {
		fmt.Fprintf(logw, "===== SEED %d len %d =====\n", seed, seqLen)
	}
	for i := 0; i < seqLen; i++ {
		o := g.genOp(i)
		wOut := runWb(wb, o)
		nOut := runNb(nb, o)
		if logw != nil {
			fmt.Fprintf(logw, "[%04d] IN  %s\n", i, formatOp(o))
			fmt.Fprintf(logw, "       OUT treap=%s naive=%s\n", formatOutcome(wOut), formatOutcome(nOut))
		}
		if len(wOut.order) == 0 {
			wOut.order = nil
		}
		if len(nOut.order) == 0 {
			nOut.order = nil
		}
		if wOut.errKind != nOut.errKind || wOut.holder != nOut.holder ||
			wOut.remain != nOut.remain || wOut.target != nOut.target ||
			!reflect.DeepEqual(wOut.order, nOut.order) || wOut.rank != nOut.rank || wOut.rev != nOut.rev {
			t.Fatalf("seed=%d step=%d outcome mismatch\nIN %s\nW  %+v\nN  %+v", seed, i, formatOp(o), wOut, nOut)
		}
		// 更新生成器视图：仅以 treap 侧结果（已与 naive 一致）为准。
		now := o.now
		_ = now
		g.advance(o, wOut.errKind == 0, wb)
		ws := wbSnapToCanon(wb.Snapshot(clampTime(o.now)))
		ns := nbSnapToCanon(nb.Snapshot(clampTime(o.now)))
		if d := ws.diff(ns); d != "" {
			if logw != nil {
				fmt.Fprintf(logw, "       SNAPSHOT DIVERGENCE: %s\n", d)
			}
			t.Fatalf("seed=%d step=%d snapshot mismatch: %s\nIN %s", seed, i, d, formatOp(o))
		}
		if logw != nil {
			fmt.Fprintf(logw, "       JUDGE equivalent; order=[%s] rev=%d\n", ws.order, ws.rev)
		}
	}
}

func clampTime(x int64) int64 {
	if x < 0 {
		return 0
	}
	if x > 1_000_000_000_000 {
		return 1_000_000_000_000
	}
	return x
}

func TestRandomDifferential(t *testing.T) {
	logPath := filepath.Join("..", "..", "testlogs", "diff.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	logw := bufio.NewWriter(f)
	defer logw.Flush()

	for s := 0; s < diffSequences; s++ {
		seed := uint64(s*2654435761 + 12345)
		runOneSequence(t, seed, 60, logw)
	}
	t.Logf("differential sequences=%d, log=%s", diffSequences, logPath)
}
