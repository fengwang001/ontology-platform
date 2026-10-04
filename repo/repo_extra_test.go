package repo

import (
	"sync"
	"testing"

	"ontology/haircut"
)

func TestRepoValidationAndRejectOrder(t *testing.T) {
	s := newExample(t)
	mustOK(t, s.PledgeIn(0, []byte("u"), []byte("A"), 105), "in")
	// 参数非法
	wantErr(t, s.Repo(-1, []byte("r"), []byte("u"), 1, 1, 0), haircut.ErrInvalid, "day")
	wantErr(t, s.Repo(0, nil, []byte("u"), 1, 1, 0), haircut.ErrInvalid, "nil id")
	wantErr(t, s.Repo(0, []byte("r"), nil, 1, 1, 0), haircut.ErrInvalid, "nil acct")
	wantErr(t, s.Repo(0, []byte("r"), []byte("u"), 0, 1, 0), haircut.ErrInvalid, "amount 0")
	wantErr(t, s.Repo(0, []byte("r"), []byte("u"), 1, 0, 0), haircut.ErrInvalid, "days 0")
	wantErr(t, s.Repo(0, []byte("r"), []byte("u"), 1, 366, 0), haircut.ErrInvalid, "days 366")
	wantErr(t, s.Repo(0, []byte("r"), []byte("u"), 1, 1, -1), haircut.ErrInvalid, "rate -1")
	wantErr(t, s.Repo(0, []byte("r"), []byte("u"), 1, 1, 10001), haircut.ErrInvalid, "rate 10001")
	// 日期回退优先于重复/不存在
	mustOK(t, s.Repo(2, []byte("r1"), []byte("u"), 100, 1, 0), "r1")
	wantErr(t, s.Repo(1, []byte("r1"), []byte("u"), 1, 1, 0), haircut.ErrDayBackward, "backward before dup")
	// 重复编号（先结算后判定，但无到期项，结果一致）
	wantErr(t, s.Repo(2, []byte("r1"), []byte("u"), 1, 1, 0), haircut.ErrDupRepo, "dup")
	// 不存在账户优先于欠库与额度
	wantErr(t, s.Repo(2, []byte("r2"), []byte("ghost"), 1, 1, 0), haircut.ErrNoAccount, "no acct")
	// 欠库优先于标准券不足：r1 占用 1，Cap94；下调 A 至 0 -> 欠库
	mustOK(t, s.SetRate(2, []byte("A"), 0), "cut to 0")
	wantErr(t, s.Repo(2, []byte("r3"), []byte("u"), 1, 1, 0), haircut.ErrDeficit, "deficit over cap")
}

func TestSettlementSurvivesSubsequentReject(t *testing.T) {
	// 第 7 日入口：先结算到期回购，之后业务被标准券不足拒绝——结算结果仍保留。
	s := newExample(t)
	mustOK(t, s.PledgeIn(0, []byte("u"), []byte("A"), 105), "in")
	mustOK(t, s.Repo(0, []byte("r1"), []byte("u"), 1000, 7, 0), "repo due day7, occ10")
	// day7 入口做 PledgeOut：结算先执行（购回成功、释放占用），出库业务随后执行——
	// 用一个结算前必然欠库、因而会被拒的场景更能说明"结算先于业务"：
	// 这里直接验证：到期购回后 Use=0，出库 A 全部成功，状态已了结。
	mustOK(t, s.PledgeOut(7, []byte("u"), []byte("A"), 105), "out after settle")
	rp := s.GetRepo([]byte("r1"))
	if rp.Status != StatusRepaid {
		t.Fatalf("r1 status=%d want repaid", rp.Status)
	}
	a := s.Acct([]byte("u"))
	if a.Cash != 0 || a.Use != 0 || a.Pledged["A"] != 0 || a.Avail["A"] != 200 {
		t.Fatalf("cash=%d use=%d pledgedA=%d availA=%d", a.Cash, a.Use, a.Pledged["A"], a.Avail["A"])
	}

	// 拒绝不回滚：第 8 日用一个库存不足的出库触发拒绝，day 仍推进到 8、已了结状态不变
	wantErr(t, s.PledgeOut(8, []byte("u"), []byte("B"), 9999), haircut.ErrStock, "stock reject")
	if day := s.Day(); day != 8 {
		t.Fatalf("day=%d want 8 (reject must still advance)", day)
	}
	if rp := s.GetRepo([]byte("r1")); rp.Status != StatusRepaid {
		t.Fatalf("status changed to %d after later reject", rp.Status)
	}
}

func TestConcurrentSerialEquivalence(t *testing.T) {
	// 并发对独立账户做无冲突的入出库与回购，串行语义下全部成功且状态守恒。
	s := New()
	const n = 40
	mustOK(t, s.AddBond(0, []byte("A"), 100, 100), "add once")
	for i := 0; i < n; i++ {
		acct := []byte("u" + padCode(i))
		mustOK(t, s.Credit(0, acct, []byte("A"), 100), "seed credit")
		mustOK(t, s.PledgeIn(0, acct, []byte("A"), 100), "seed pledge")
	}
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		i := i
		acct := []byte("u" + padCode(i))
		id := []byte("r" + padCode(i))
		wg.Add(1)
		go func() {
			defer wg.Done()
			// 同一天、独立账户、独立编号：任意串行次序结果相同。
			if err := s.Repo(1, id, acct, 10000, 7, 0); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent op: %v", err)
	}
	for i := 0; i < n; i++ {
		a := s.Acct([]byte("u" + padCode(i)))
		if a == nil || a.Avail["A"]+a.Pledged["A"] != 100 {
			t.Fatalf("acct %d holding total=%v", i, a)
		}
		if a.Pledged["A"] != 100 || a.Cap != 100 || a.Use != 100 {
			t.Fatalf("acct %d pledged=%d cap=%d use=%d", i, a.Pledged["A"], a.Cap, a.Use)
		}
		if a.Cash != 10000 {
			t.Fatalf("acct %d cash=%d want 10000", i, a.Cash)
		}
	}
	if n2 := s.RepoCount(); n2 != n {
		t.Fatalf("repo count=%d want %d", n2, n)
	}
}

func padCode(i int) string {
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	if i < len(digits) {
		return string(digits[i])
	}
	return string(digits[i/len(digits)]) + string(digits[i%len(digits)])
}
