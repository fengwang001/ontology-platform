package greenreg

import (
	"errors"
	"testing"
)

func errCode(err error) ErrCode {
	var re *RegistryError
	if errors.As(err, &re) {
		return re.Code
	}
	return 0
}

func errFailCert(err error) (int64, bool) {
	var re *RegistryError
	if errors.As(err, &re) && re.Fail != nil {
		return re.Fail.Cert, true
	}
	return 0, false
}

func mustIssue(t *testing.T, r *Registry, id string, p, q int64) []int64 {
	t.Helper()
	issued, _, err := r.RegisterGeneration(id, p, q)
	if err != nil {
		t.Fatalf("issue %s p=%d q=%d: %v", id, p, q, err)
	}
	return issued
}

// 余量恰好凑满一单位：unit=5，先 3 张不出证，再 2 恰好出 1 张，余 0。
func TestRemainderFillsExactlyOneUnit(t *testing.T) {
	r := New(Config{UnitQty: 5, MaxAgePeriods: 10})
	if err := r.RegisterFacility("F", "h", 0); err != nil {
		t.Fatal(err)
	}
	if got := mustIssue(t, r, "F", 0, 3); len(got) != 0 {
		t.Fatalf("first 3 energy should issue 0, got %v", got)
	}
	got := mustIssue(t, r, "F", 1, 2)
	if len(got) != 1 {
		t.Fatalf("3+2 should issue exactly 1 cert, got %v", got)
	}
	got2 := mustIssue(t, r, "F", 2, 5)
	if len(got2) != 1 || got2[0] != 2 {
		t.Fatalf("next issue must be serial 2, got %v", got2)
	}
}

// 发电期恰等于用电期，与恰在最大年限边界（取等允许）。
func TestRetirePeriodBoundaries(t *testing.T) {
	r := New(Config{UnitQty: 1, MaxAgePeriods: 3})
	mustFac(t, r, "F", 0)
	eq := mustIssue(t, r, "F", 5, 1)   // generation == usage
	edge := mustIssue(t, r, "F", 2, 1) // 5-3 = 2, boundary allowed
	out := mustIssue(t, r, "F", 1, 1)  // 1 < 2, outside
	if err := r.RegisterUsage("u", 5, 3); err != nil {
		t.Fatal(err)
	}
	if err := r.Transfer("h", "u", []int64{eq[0], edge[0], out[0]}); err != nil {
		t.Fatal(err)
	}
	if err := r.Retire("u", 5, []int64{eq[0], edge[0]}); err != nil {
		t.Fatalf("equal and boundary certificates must retire: %v", err)
	}
	err := r.Retire("u", 5, []int64{out[0]})
	if errCode(err) != ErrPeriodMismatch {
		t.Fatalf("outside window = ErrPeriodMismatch, got %v", err)
	}
}

func mustFac(t *testing.T, r *Registry, id string, start int64) {
	t.Helper()
	if err := r.RegisterFacility(id, "h", start); err != nil {
		t.Fatal(err)
	}
}

// 注销总量恰等于用电量（取等允许），再多一张则拒绝。
func TestRetireExactlyUsage(t *testing.T) {
	r := New(Config{UnitQty: 1, MaxAgePeriods: 10})
	mustFac(t, r, "F", 0)
	c := mustIssue(t, r, "F", 0, 2)
	if err := r.RegisterUsage("u", 1, 2); err != nil {
		t.Fatal(err)
	}
	if err := r.Transfer("h", "u", c); err != nil {
		t.Fatal(err)
	}
	if err := r.Retire("u", 1, c); err != nil {
		t.Fatalf("retiring exactly usage must pass: %v", err)
	}
	extra := mustIssue(t, r, "F", 1, 1)
	if err := r.Transfer("h", "u", extra); err != nil {
		t.Fatal(err)
	}
	err := r.Retire("u", 1, extra)
	if errCode(err) != ErrOverUsage {
		t.Fatalf("one over = ErrOverUsage, got %v", err)
	}
}

// 撤销顺序：先持有（序号大到小），后已注销（注销时刻晚到早），并产生声明失效事件。
func TestRevocationOrderAndVoidEvents(t *testing.T) {
	r := New(Config{UnitQty: 1, MaxAgePeriods: 10})
	mustFac(t, r, "F", 0)
	c := mustIssue(t, r, "F", 0, 4) // 1,2,3,4
	if err := r.RegisterUsage("u", 1, 2); err != nil {
		t.Fatal(err)
	}
	if err := r.Transfer("h", "u", c); err != nil {
		t.Fatal(err)
	}
	if err := r.Retire("u", 1, []int64{c[0], c[1]}); err != nil { // retire 1 then 2
		t.Fatal(err)
	}
	// correct 4 -> 1: live must fall from 4 to 1. Held 4,3 revoked first;
	// then retired cert 2 (later retirement moment) before 1.
	_, revoked, err := r.RegisterGeneration("F", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{4, 3, 2}
	if len(revoked) != 3 || revoked[0] != want[0] || revoked[1] != want[1] || revoked[2] != want[2] {
		t.Fatalf("revocation order = %v, want %v", revoked, want)
	}
	var voids []int64
	for _, e := range r.Events() {
		if e.Kind == EvDeclarationVoided {
			voids = append(voids, e.Cert)
			if e.User != "u" || e.UsagePeriod != 1 {
				t.Fatalf("void event user/period wrong: %+v", e)
			}
		}
	}
	if len(voids) != 1 || voids[0] != 2 {
		t.Fatalf("void events = %v, want [2]", voids)
	}
}

// 向上修正不恢复已撤销证书，而是发新序号；若账户有余量则可能多发。
func TestUpwardCorrectionIssuesNewSerials(t *testing.T) {
	r := New(Config{UnitQty: 3, MaxAgePeriods: 10})
	mustFac(t, r, "F", 0)
	c := mustIssue(t, r, "F", 0, 6) // serials 1,2; remainder 0
	if len(c) != 2 {
		t.Fatalf("setup: want 2 certs, got %v", c)
	}
	_, revoked, err := r.RegisterGeneration("F", 0, 3) // want 1 cert -> revoke serial 2
	if err != nil || len(revoked) != 1 || revoked[0] != 2 {
		t.Fatalf("down correction: %v %v", revoked, err)
	}
	again, _, err := r.RegisterGeneration("F", 0, 6) // want 2 -> issue serial 3 only
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 1 || again[0] != 3 {
		t.Fatalf("up correction must issue new serial 3 only, got %v", again)
	}
	if r.pool.get(2).Status != StatusRevoked {
		t.Fatal("revoked cert 2 must stay revoked")
	}
	if c[0] != 1 {
		t.Fatal("serial continuity broken")
	}
}

// 用电量修正恰等于已注销量允许，低于则报「低于已注销量」。
func TestUsageCorrectionAtRetiredLevel(t *testing.T) {
	r := New(Config{UnitQty: 1, MaxAgePeriods: 10})
	mustFac(t, r, "F", 0)
	c := mustIssue(t, r, "F", 0, 2)
	if err := r.RegisterUsage("u", 1, 5); err != nil {
		t.Fatal(err)
	}
	if err := r.Transfer("h", "u", c); err != nil {
		t.Fatal(err)
	}
	if err := r.Retire("u", 1, c); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterUsage("u", 1, 2); err != nil {
		t.Fatalf("correction to exactly retired must pass: %v", err)
	}
	err := r.RegisterUsage("u", 1, 1)
	if errCode(err) != ErrBelowRetired {
		t.Fatalf("below retired = ErrBelowRetired, got %v", err)
	}
}

// 资格终止期与已核发冲突；且不可提前。
func TestTerminationConflicts(t *testing.T) {
	r := New(Config{UnitQty: 1, MaxAgePeriods: 10})
	mustFac(t, r, "F", 0)
	mustIssue(t, r, "F", 3, 1)
	err := r.SetTermination("F", 3)
	if errCode(err) != ErrConflictIssued {
		t.Fatalf("end excluding issued period = conflict, got %v", err)
	}
	if err := r.SetTermination("F", 5); err != nil {
		t.Fatalf("postponing end to max+2 must pass: %v", err)
	}
	err = r.SetTermination("F", 4) // earlier than existing end 5
	if errCode(err) != ErrInvalid {
		t.Fatalf("moving end earlier = ErrInvalid, got %v", err)
	}
	if err := r.SetTermination("F", 0); err != nil {
		t.Fatalf("clearing end must pass: %v", err)
	}
}

// 批内混合失败：按序号升序报第一个失败项。
func TestBatchReportsFirstFailure(t *testing.T) {
	r := New(Config{UnitQty: 1, MaxAgePeriods: 10})
	mustFac(t, r, "F", 0)
	c := mustIssue(t, r, "F", 0, 3) // 1,2,3
	if err := r.Transfer("h", "g", []int64{c[1]}); err != nil {
		t.Fatal(err)
	}
	// 2 belongs to g (not holder) and 3 is fine; first failing serial is 2.
	err := r.Transfer("h", "k", []int64{c[2], c[1], c[0]})
	if errCode(err) != ErrNotHolder {
		t.Fatalf("want ErrNotHolder, got %v", err)
	}
	if s, ok := errFailCert(err); !ok || s != c[1] {
		t.Fatalf("first failing cert = %d, want %d", s, c[1])
	}
}

// 拒绝次序：状态不允许 > 非持有人 > 期限不符。
func TestRejectionPrecedence(t *testing.T) {
	r := New(Config{UnitQty: 1, MaxAgePeriods: 0})
	mustFac(t, r, "F", 0)
	// cert 1: retired (state), cert 2: held by other after transfer,
	// cert 3: held by u but generation period 1 > usage 0 (window fail)
	c := mustIssue(t, r, "F", 0, 3)
	mustIssue(t, r, "F", 1, 1) // serial 4
	_ = c
	all := []int64{1, 2, 3}
	if err := r.RegisterUsage("u", 0, 10); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterUsage("h", 0, 10); err != nil {
		t.Fatal(err)
	}
	if err := r.Retire("h", 0, []int64{1}); err != nil {
		t.Fatal(err)
	}
	if err := r.Transfer("h", "g", []int64{2}); err != nil {
		t.Fatal(err)
	}
	if err := r.Transfer("h", "u", []int64{3}); err != nil {
		t.Fatal(err)
	}
	if err := r.Transfer("h", "u", []int64{4}); err != nil {
		t.Fatal(err)
	}
	err := r.Retire("u", 0, all)
	if errCode(err) != ErrStateNotAllowed {
		t.Fatalf("state must precede holder/window, got %v", err)
	}
	// remove the retired one: now holder failure on cert 2 must win over window on 3
	err = r.Retire("u", 0, []int64{2, 4})
	if errCode(err) != ErrNotHolder {
		t.Fatalf("holder must precede window, got %v", err)
	}
	// only window failure remains
	err = r.Retire("u", 0, []int64{4})
	if errCode(err) != ErrPeriodMismatch {
		t.Fatalf("window failure expected, got %v", err)
	}
	// self-transfer is the top-level parameter illegality
	if err := r.Transfer("h", "h", []int64{1}); errCode(err) != ErrInvalid {
		t.Fatalf("self transfer = ErrInvalid, got %v", err)
	}
}
