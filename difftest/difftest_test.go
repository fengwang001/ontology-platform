// 对拍：实现与朴素模拟在 2000 组随机操作序列上逐步比对，
// 日志打印每步的输入、输出与判定依据（go test -v 可见）。
package difftest

import (
	"math/rand"
	"slices"
	"sort"
	"testing"

	"ontology/erase"
	"ontology/hold"
	"ontology/restore"
)

func TestDifferentialRandom(t *testing.T) {
	for seq := 0; seq < 2000; seq++ {
		runSeq(t, seq)
		if t.Failed() {
			t.Fatalf("seq=%d 出现分歧，停止后续序列", seq)
		}
	}
}

func pickNow(rng *rand.Rand, cur *int64) int64 {
	switch r := rng.Intn(100); {
	case r < 70:
		*cur += rng.Int63n(15)
	case r < 85:
		// 与上一时刻相同（合法）
	case r < 95:
		if *cur > 0 {
			return *cur - 1 - rng.Int63n(*cur) // 时钟回退
		}
	default:
		return erase.MaxNow + 1 // 参数越界
	}
	return *cur
}

func pickRole(rng *rand.Rand, want int) int {
	if rng.Intn(100) < 75 {
		return want
	}
	return rng.Intn(5) // 0..4，含非法角色
}

func pickS(rng *rand.Rand, S int) int {
	switch rng.Intn(10) {
	case 0:
		return 0
	case 1:
		return S + 1
	default:
		return 1 + rng.Intn(S)
	}
}

func pickID(rng *rand.Rand, count int) int {
	if count == 0 {
		return rng.Intn(3)
	}
	switch rng.Intn(10) {
	case 0:
		return 0
	case 1:
		return count + 1 + rng.Intn(2)
	default:
		return 1 + rng.Intn(count)
	}
}

func runSeq(t *testing.T, seq int) {
	t.Helper()
	rng := rand.New(rand.NewSource(int64(seq)*1_000_003 + 17))
	S := 1 + rng.Intn(3)
	T := int64(1 + rng.Intn(60))
	l, err := erase.New(S, T)
	if err != nil {
		t.Fatal(err)
	}
	hs := hold.New(l)
	rs := restore.New(l)
	n := newNaive(S, T)
	t.Logf("seq=%04d 构造 S=%d T=%d", seq, S, T)
	cur := int64(0)
	ops := 20 + rng.Intn(60)
	for i := 0; i < ops; i++ {
		now := pickNow(rng, &cur)
		kind := rng.Intn(100)
		switch {
		case kind < 25:
			role, sub := pickRole(rng, erase.RolePrivacy), int64(1+rng.Intn(6))
			gotID, gotErr := l.Request(role, sub, now)
			wantID, wantErr, why := n.request(role, sub, now)
			if gotErr != wantErr || (wantErr == nil && gotID != wantID) {
				t.Fatalf("seq=%d op=%d Request(%d,%d,%d): got (%d,%v), naive (%d,%v)",
					seq, i, role, sub, now, gotID, gotErr, wantID, wantErr)
			}
			t.Logf("seq=%04d op=%02d Request(role=%d,sub=%d,now=%d) => id=%d err=%v | 依据: %s",
				seq, i, role, sub, now, gotID, gotErr, why)
		case kind < 45:
			role, e, s := pickRole(rng, erase.RoleOps), pickID(rng, len(n.eras)), pickS(rng, S)
			gotErr := l.Ack(role, e, s, now)
			wantErr, why := n.ack(role, e, s, now)
			if gotErr != wantErr {
				t.Fatalf("seq=%d op=%d Ack(%d,%d,%d,%d): got %v, naive %v",
					seq, i, role, e, s, now, gotErr, wantErr)
			}
			t.Logf("seq=%04d op=%02d Ack(role=%d,e=%d,s=%d,now=%d) => err=%v | 依据: %s",
				seq, i, role, e, s, now, gotErr, why)
		case kind < 53:
			role, sub := pickRole(rng, erase.RoleLegal), int64(1+rng.Intn(6))
			gotErr := hs.Hold(role, sub, now)
			wantErr, why := n.hold(role, sub, now)
			if gotErr != wantErr {
				t.Fatalf("seq=%d op=%d Hold(%d,%d,%d): got %v, naive %v",
					seq, i, role, sub, now, gotErr, wantErr)
			}
			t.Logf("seq=%04d op=%02d Hold(role=%d,sub=%d,now=%d) => err=%v | 依据: %s",
				seq, i, role, sub, now, gotErr, why)
		case kind < 61:
			role, sub := pickRole(rng, erase.RoleLegal), int64(1+rng.Intn(6))
			gotErr := hs.Release(role, sub, now)
			wantErr, why := n.release(role, sub, now)
			if gotErr != wantErr {
				t.Fatalf("seq=%d op=%d Release(%d,%d,%d): got %v, naive %v",
					seq, i, role, sub, now, gotErr, wantErr)
			}
			t.Logf("seq=%04d op=%02d Release(role=%d,sub=%d,now=%d) => err=%v | 依据: %s",
				seq, i, role, sub, now, gotErr, why)
		case kind < 69:
			role, s := pickRole(rng, erase.RoleOps), pickS(rng, S)
			gotID, gotErr := rs.Backup(role, s, now)
			wantID, wantErr, why := n.backup(role, s, now)
			if gotErr != wantErr || (wantErr == nil && gotID != wantID) {
				t.Fatalf("seq=%d op=%d Backup(%d,%d,%d): got (%d,%v), naive (%d,%v)",
					seq, i, role, s, now, gotID, gotErr, wantID, wantErr)
			}
			t.Logf("seq=%04d op=%02d Backup(role=%d,s=%d,now=%d) => b=%d err=%v | 依据: %s",
				seq, i, role, s, now, gotID, gotErr, why)
		case kind < 77:
			role, s, b := pickRole(rng, erase.RoleOps), pickS(rng, S), pickID(rng, len(n.backups))
			gotL, gotErr := rs.Restore(role, s, b, now)
			wantL, wantErr, why := n.restore(role, s, b, now)
			if gotErr != wantErr || (wantErr == nil && !slices.Equal(gotL, wantL)) {
				t.Fatalf("seq=%d op=%d Restore(%d,%d,%d,%d): got (%v,%v), naive (%v,%v)",
					seq, i, role, s, b, now, gotL, gotErr, wantL, wantErr)
			}
			t.Logf("seq=%04d op=%02d Restore(role=%d,s=%d,b=%d,now=%d) => L=%v err=%v | 依据: %s",
				seq, i, role, s, b, now, gotL, gotErr, why)
		case kind < 85:
			role, s, e := pickRole(rng, erase.RoleOps), pickS(rng, S), pickID(rng, len(n.eras))
			gotErr := rs.ReapplyDone(role, s, e, now)
			wantErr, why := n.reapply(role, s, e, now)
			if gotErr != wantErr {
				t.Fatalf("seq=%d op=%d ReapplyDone(%d,%d,%d,%d): got %v, naive %v",
					seq, i, role, s, e, now, gotErr, wantErr)
			}
			t.Logf("seq=%04d op=%02d ReapplyDone(role=%d,s=%d,e=%d,now=%d) => err=%v | 依据: %s",
				seq, i, role, s, e, now, gotErr, why)
		case kind < 93:
			got := l.Overdue(now)
			want, why := n.overdue(now)
			if !equalOverdue(got, want) {
				t.Fatalf("seq=%d op=%d Overdue(%d): got %+v, naive %+v", seq, i, now, got, want)
			}
			t.Logf("seq=%04d op=%02d Overdue(now=%d) => %+v | 依据: %s", seq, i, now, got, why)
		default:
			s := pickS(rng, S)
			gotErr := rs.Read(s)
			wantErr, why := n.read(s)
			if gotErr != wantErr {
				t.Fatalf("seq=%d op=%d Read(%d): got %v, naive %v", seq, i, s, gotErr, wantErr)
			}
			t.Logf("seq=%04d op=%02d Read(s=%d) => err=%v | 依据: %s", seq, i, s, gotErr, why)
		}
		checkState(t, l, n, S, seq, i)
	}
}

func equalOverdue(a, b []erase.OverdueEntry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID || !slices.Equal(a[i].Pending, b[i].Pending) {
			return false
		}
	}
	return true
}

// checkState 全量比对实现与朴素模拟的状态，并校验三条不变式。
func checkState(t *testing.T, l *erase.Ledger, n *naive, S, seq, op int) {
	t.Helper()
	_ = l.View(func(c *erase.Core) error {
		if c.MaxNow() != n.maxNow {
			t.Fatalf("seq=%d op=%d: maxNow=%d, naive=%d", seq, op, c.MaxNow(), n.maxNow)
		}
		if c.NumErasures() != len(n.eras) {
			t.Fatalf("seq=%d op=%d: 擦除单数=%d, naive=%d", seq, op, c.NumErasures(), len(n.eras))
		}
		for i := 1; i <= c.NumErasures(); i++ {
			e := c.Erasure(i)
			ne := n.eras[i-1]
			if e.Status != ne.status || e.Deadline != ne.deadline || e.Subject != ne.subject {
				t.Fatalf("seq=%d op=%d: e%d 状态分歧 got(%v,%d,%d) naive(%v,%d,%d)",
					seq, op, i, e.Status, e.Deadline, e.Subject, ne.status, ne.deadline, ne.subject)
			}
			acked := 0
			for s := 1; s <= S; s++ {
				want := int64(-1)
				if v, ok := ne.acks[s]; ok {
					want = v
				}
				if e.AckAt[s-1] != want {
					t.Fatalf("seq=%d op=%d: e%d ack(s=%d)=%d, naive=%d", seq, op, i, s, e.AckAt[s-1], want)
				}
				if want >= 0 {
					acked++
				}
			}
			if e.Status == erase.Done && acked != S {
				t.Fatalf("seq=%d op=%d: 不变式违反：e%d Done 但缺 ack", seq, op, i)
			}
			if e.Status == erase.Deferred && acked != 0 {
				t.Fatalf("seq=%d op=%d: 不变式违反：e%d Deferred 但有 ack", seq, op, i)
			}
		}
		for sub := int64(1); sub <= 6; sub++ {
			if c.IsHeld(sub) != n.held[sub] {
				t.Fatalf("seq=%d op=%d: held(%d) 分歧", seq, op, sub)
			}
		}
		if c.NumBackups() != len(n.backups) {
			t.Fatalf("seq=%d op=%d: 备份点数=%d, naive=%d", seq, op, c.NumBackups(), len(n.backups))
		}
		for b := 1; b <= c.NumBackups(); b++ {
			bk, _ := c.GetBackup(b)
			if bk.System != n.backups[b-1].sys || bk.Tb != n.backups[b-1].tb {
				t.Fatalf("seq=%d op=%d: b%d 分歧", seq, op, b)
			}
		}
		for s := 1; s <= S; s++ {
			st := c.SystemStatus(s)
			wantSt := erase.Ready
			if n.restoring[s] {
				wantSt = erase.Restoring
			}
			if st != wantSt {
				t.Fatalf("seq=%d op=%d: s%d 状态=%v, naive=%v", seq, op, s, st, wantSt)
			}
			todo := c.Todo(s)
			if st == erase.Restoring && len(todo) == 0 {
				t.Fatalf("seq=%d op=%d: 不变式违反：s%d Restoring 但待办为空", seq, op, s)
			}
			var wantTodo []int
			for id := range n.todo[s] {
				wantTodo = append(wantTodo, id)
			}
			sort.Ints(wantTodo)
			if !slices.Equal(todo, wantTodo) {
				t.Fatalf("seq=%d op=%d: s%d 待办=%v, naive=%v", seq, op, s, todo, wantTodo)
			}
		}
		return nil
	})
}
