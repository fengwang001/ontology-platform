package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"ontology/api"
	"ontology/lease"
	"ontology/mgr"
)

func chkErr(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
}

// 第三节七步推导的逐步核验（TTL=10）。
func TestSevenSteps(t *testing.T) {
	a := api.New(10)
	steps := []struct {
		acq      bool
		own      string
		tok, now int
		wantErr  error
		wTok     int
		wOwn     string
		wExp     int
	}{
		{true, "A", 0, 0, nil, 1, "A", 10},                  // S1
		{false, "", 1, 5, nil, 1, "A", 15},                  // S2
		{false, "", 1, 12, nil, 1, "A", 22},                 // S3
		{true, "B", 0, 20, nil, 2, "B", 30},                 // S4
		{false, "", 1, 21, lease.ErrStaleToken, 2, "B", 30}, // S5
		{false, "", 2, 31, lease.ErrExpired, 2, "B", 30},    // S6
	}
	for i, s := range steps {
		var err error
		if s.acq {
			var tok int
			if tok, err = a.Acquire("L", s.own, s.now); err == nil && tok != s.wTok {
				t.Fatalf("S%d 返回 token=%d, want %d", i+1, tok, s.wTok)
			}
		} else {
			err = a.Renew("L", s.tok, s.now)
		}
		chkErr(t, err, s.wantErr)
		o, k, e, _ := a.Lookup("L")
		if o != s.wOwn || k != s.wTok || e != s.wExp {
			t.Fatalf("S%d 状态=%s/%d/%d, want %s/%d/%d", i+1, o, k, e, s.wOwn, s.wTok, s.wExp)
		}
	}
	if !a.Expired("L", 30) || a.Expired("L", 29) { // S7：左闭边界
		t.Fatal("S7: now==expiry 应判到期，now<expiry 不应")
	}
}

// 不变量1：随机操作序列下，Expired/ExpiredAll 与朴素模型逐字对拍。
func TestExpiredMatchesNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for _, steps := range []int{50, 500, 5000} {
		a := api.New(10)
		tok, exp := map[string]int{}, map[string]int{}
		for now := 0; now < steps; now++ {
			name := fmt.Sprintf("n%d", rng.Intn(8))
			if rng.Intn(2) == 0 {
				got, err := a.Acquire(name, "o", now)
				if err != nil {
					t.Fatalf("Acquire: %v", err)
				}
				tok[name]++
				exp[name] = now + 10
				if got != tok[name] {
					t.Fatalf("token=%d, want %d", got, tok[name])
				}
			} else {
				useTok := tok[name]
				if rng.Intn(2) == 0 {
					useTok++ // 故意用错 token
				}
				err := a.Renew(name, useTok, now)
				cur, ok := tok[name]
				switch {
				case !ok:
					chkErr(t, err, mgr.ErrNotFound)
				case useTok != cur:
					chkErr(t, err, lease.ErrStaleToken)
				case now >= exp[name]:
					chkErr(t, err, lease.ErrExpired)
				default:
					chkErr(t, err, nil)
					exp[name] = now + 10
				}
			}
			wantAll := map[string]bool{}
			for n, e := range exp {
				if a.Expired(n, now) != (now >= e) {
					t.Fatalf("now=%d %s 与朴素不一致", now, n)
				}
				wantAll[n] = now >= e
			}
			for _, n := range a.ExpiredAll(now) {
				if !wantAll[n] {
					t.Fatalf("ExpiredAll 多报 %s", n)
				}
				delete(wantAll, n)
			}
			for n, w := range wantAll {
				if w {
					t.Fatalf("ExpiredAll 漏报 %s", n)
				}
			}
		}
	}
}

// 不变量4 + 故障注入：四类错误可判定且互不相同，被拒后状态不变、可继续用。
func TestFailureLeavesNoTrace(t *testing.T) {
	a := api.New(10)
	a.Acquire("x", "A", 0) // token=1 expiry=10
	_, e1 := a.Acquire("", "o", 0)
	chkErr(t, a.Renew("", 1, 0), e1) // 空名在 Acquire/Renew 是同一类
	// 依次：空名字、未授予的名字、token 不匹配、已到期
	errs := []error{e1, a.Renew("ghost", 1, 0), a.Renew("x", 99, 1), a.Renew("x", 1, 10)}
	for i, err := range errs {
		if err == nil {
			t.Fatalf("第 %d 类故障未报错", i)
		}
		for j := i + 1; j < len(errs); j++ {
			if errors.Is(err, errs[j]) {
				t.Fatalf("错误 %d 与 %d 不可区分: %v vs %v", i, j, err, errs[j])
			}
		}
	}
	o, k, e, _ := a.Lookup("x")
	if o != "A" || k != 1 || e != 10 {
		t.Fatalf("被拒后状态变了: %s/%d/%d", o, k, e)
	}
	chkErr(t, a.Renew("x", 1, 5), nil) // 拒绝后仍可正常使用
}

func TestSelfCheck(t *testing.T) {
	chkErr(t, api.New(10).SelfCheck(), nil)
}
