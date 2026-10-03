package xfer

import (
	"errors"
	"flag"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

var diffLog = flag.Bool("difflog", false, "print every random differential op: input/output/reason")

type opKind int

const (
	opCreate opKind = iota
	opLimit
	opPut
	opDelete
	opBegin
	opEnd
	opOffer
	opCancel
	opAccept
	opRecover
	opCrash
	opN
)

type op struct {
	kind   opKind
	a, b   string
	c      string
	num    int64
	now    int64
	crashK int
}

func (o op) String() string {
	switch o.kind {
	case opCreate:
		return fmt.Sprintf("CreateBucket(%q,%q,now=%d)", o.a, o.b, o.now)
	case opLimit:
		return fmt.Sprintf("SetLimit(%q,q=%d)", o.a, o.num)
	case opPut:
		return fmt.Sprintf("Put(by=%q,b=%q,size=%d,now=%d)", o.a, o.b, o.num, o.now)
	case opDelete:
		return fmt.Sprintf("Delete(by=%q,b=%q,size=%d,now=%d)", o.a, o.b, o.num, o.now)
	case opBegin:
		return fmt.Sprintf("BeginUpload(by=%q,b=%q,now=%d)", o.a, o.b, o.now)
	case opEnd:
		return fmt.Sprintf("EndUpload(by=%q,b=%q,now=%d)", o.a, o.b, o.now)
	case opOffer:
		return fmt.Sprintf("Offer(b=%q,from=%q,to=%q,ttl=%d,now=%d)", o.b, o.a, o.c, o.num, o.now)
	case opCancel:
		return fmt.Sprintf("Cancel(b=%q,by=%q,now=%d)", o.b, o.a, o.now)
	case opAccept:
		return fmt.Sprintf("Accept(b=%q,by=%q,now=%d,crashK=%d)", o.b, o.a, o.now, o.crashK)
	case opRecover:
		return "Recover()"
	case opCrash:
		return fmt.Sprintf("SetCrashAfter(%d)", o.crashK)
	}
	return "?"
}

func genOp(rng *rand.Rand, tenants, buckets []string, now int64) op {
	pick := func(xs []string) string {
		if len(xs) == 0 {
			return "T0"
		}
		return xs[rng.Intn(len(xs))]
	}
	t1 := pick(tenants)
	t2 := pick(tenants)
	bk := pick(buckets)
	if rng.Intn(6) == 0 {
		t2 = fmt.Sprintf("T%d", rng.Intn(4))
	}
	k := opKind(rng.Intn(int(opN)))
	switch k {
	case opCreate:
		name := fmt.Sprintf("b%d", rng.Intn(6))
		return op{kind: k, a: fmt.Sprintf("T%d", rng.Intn(4)), b: name, now: now}
	case opLimit:
		return op{kind: k, a: t1, num: int64(rng.Intn(120))}
	case opPut:
		return op{kind: k, a: t1, b: bk, num: int64(1 + rng.Intn(40)), now: now}
	case opDelete:
		return op{kind: k, a: t1, b: bk, num: int64(1 + rng.Intn(50)), now: now}
	case opBegin, opEnd:
		return op{kind: k, a: t1, b: bk, now: now}
	case opOffer:
		return op{kind: k, a: t1, b: bk, c: t2,
			num: int64(1 + rng.Intn(8)), now: now}
	case opCancel, opAccept:
		return op{kind: k, a: t1, b: bk, now: now,
			crashK: rng.Intn(6)}
	case opRecover:
		return op{kind: k}
	case opCrash:
		return op{kind: k, crashK: rng.Intn(6)}
	}
	return op{}
}

func runReal(s *Service, o op) error {
	switch o.kind {
	case opCreate:
		return s.CreateBucket(o.a, o.b, o.now)
	case opLimit:
		s.SetLimit(o.a, o.num)
		return nil
	case opPut:
		return s.Put(o.a, o.b, o.num, o.now)
	case opDelete:
		return s.Delete(o.a, o.b, o.num, o.now)
	case opBegin:
		return s.BeginUpload(o.a, o.b, o.now)
	case opEnd:
		return s.EndUpload(o.a, o.b, o.now)
	case opOffer:
		return s.Offer(o.b, o.a, o.c, o.num, o.now)
	case opCancel:
		return s.Cancel(o.b, o.a, o.now)
	case opAccept:
		return s.Accept(o.b, o.a, o.now)
	case opRecover:
		s.Recover()
		return nil
	case opCrash:
		s.SetCrashAfter(o.crashK)
		return nil
	}
	return nil
}

func runNaive(n *naive, o op) error {
	switch o.kind {
	case opCreate:
		return n.CreateBucket(o.a, o.b, o.now)
	case opLimit:
		n.SetLimit(o.a, o.num)
		return nil
	case opPut:
		return n.Put(o.a, o.b, o.num, o.now)
	case opDelete:
		return n.Delete(o.a, o.b, o.num, o.now)
	case opBegin:
		return n.BeginUpload(o.a, o.b, o.now)
	case opEnd:
		return n.EndUpload(o.a, o.b, o.now)
	case opOffer:
		return n.Offer(o.b, o.a, o.c, o.num, o.now)
	case opCancel:
		return n.Cancel(o.b, o.a, o.now)
	case opAccept:
		return n.Accept(o.b, o.a, o.now)
	case opRecover:
		n.Recover()
		return nil
	case opCrash:
		n.SetCrashAfter(o.crashK)
		return nil
	}
	return nil
}

func sameErr(a, b error) bool {
	return errors.Is(a, b) || (a == nil && b == nil) ||
		(a != nil && b != nil && a.Error() == b.Error())
}

func snapEqual(a Snapshot, b nSnapshot) bool {
	return reflect.DeepEqual(a.Owners, b.owners) &&
		reflect.DeepEqual(a.Bytes, b.bytes) &&
		reflect.DeepEqual(a.Uploads, b.uploads) &&
		reflect.DeepEqual(a.Used, b.used) &&
		reflect.DeepEqual(a.Limit, b.limit) &&
		reflect.DeepEqual(a.BucketN, b.count) &&
		reflect.DeepEqual(a.Offers, b.offers) &&
		a.LastNow == b.lastNow &&
		a.Recover == b.recover
}

func TestRandomDifferential(t *testing.T) {
	const sequences, length = 1500, 60
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq + 1)))
		s := New(3)
		n := newNaive(3)
		tenants := []string{"T0", "T1", "T2", "T3"}
		buckets := []string{"b0", "b1", "b2", "b3", "b4", "b5"}
		now := int64(0)
		for step := 0; step < length; step++ {
			// 时间只能前进；偶发跳跃以制造到期。
			if rng.Intn(3) == 0 {
				now += int64(rng.Intn(6))
			}
			o := genOp(rng, tenants, buckets, now)
			got := runReal(s, o)
			want := runNaive(n, o)
			reason := "accepted"
			if want != nil {
				reason = "rejected: " + want.Error()
			}
			if *diffLog {
				t.Logf("[seq=%d step=%d] %s => %v (%s)", seq, step, o, got, reason)
			}
			if !sameErr(got, want) {
				t.Fatalf("seq=%d step=%d %s: real=%v naive=%v", seq, step, o, got, want)
			}
			rs, ns := s.Snapshot(), n.Snapshot()
			if !snapEqual(rs, ns) {
				t.Fatalf("seq=%d step=%d %s:\nreal =%+v\nnaive=%+v", seq, step, o, rs, ns)
			}
			// 未崩溃时跨账本不变量。
			if !rs.Recover {
				used := map[string]int64{}
				cnt := map[string]int64{}
				for name, owner := range rs.Owners {
					used[owner] += rs.Bytes[name]
					cnt[owner]++
				}
				for tenant := range rs.Used {
					used[tenant] += 0
				}
				for tenant, u := range used {
					if rs.Used[tenant] != u || rs.BucketN[tenant] != cnt[tenant] {
						t.Fatalf("seq=%d step=%d op=%s invariant broken: tenant=%s used=%d sum=%d cnt=%d owned=%d",
							seq, step, o.String(), tenant, rs.Used[tenant], u, rs.BucketN[tenant], cnt[tenant])
					}
				}
			}
		}
		// 序列末尾若处于崩溃态，恢复后两模型仍一致且不变量成立。
		if s.NeedRecovery() {
			s.Recover()
			n.Recover()
			rs, ns := s.Snapshot(), n.Snapshot()
			if !snapEqual(rs, ns) {
				t.Fatalf("seq=%d final recover mismatch\nreal=%+v\nnaive=%+v", seq, rs, ns)
			}
		}
	}
}
