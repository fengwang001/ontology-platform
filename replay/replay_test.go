package replay

import (
	"errors"
	"testing"

	"ontology/code"
	"ontology/history"
)

func b(s string) []byte { return []byte(s) }

func st(name string) code.Step { return code.Step{Name: b(name)} }

func evsEqual(a, b []history.Event) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !a[i].Equal(b[i]) {
			return false
		}
	}
	return true
}

// seedAndRun 用 hist 预置历史后执行 Run，返回结果与最终历史。
func seedAndRun(t *testing.T, hist []history.Event, c code.Code) (Result, []history.Event, error) {
	t.Helper()
	store := history.NewStore()
	wf := b("wf")
	if len(hist) > 0 {
		if err := store.Append(wf, 0, hist); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	res, err := Run(store, wf, c)
	return res, store.Snapshot(wf), err
}

// TestSpecExamples 覆盖任务说明中的全部例子。
func TestSpecExamples(t *testing.T) {
	v2 := code.Code{st("a"), code.Branch{Pid: b("p"),
		New: []code.Item{st("b2")}, Old: []code.Item{st("b")}}, st("c")}
	cases := []struct {
		name          string
		code          code.Code
		hist          []history.Event
		wantConsumed  int
		wantContinued int
		wantFinal     []history.Event
		wantErr       error
		wantErrIndex  int
	}{
		{"空历史走新分支", v2, nil, 0, 4,
			[]history.Event{history.S(b("a")), history.M(b("p")), history.S(b("b2")), history.S(b("c"))}, nil, -1},
		{"旧代码完整历史走旧分支", v2,
			[]history.Event{history.S(b("a")), history.S(b("b")), history.S(b("c"))}, 3, 0,
			[]history.Event{history.S(b("a")), history.S(b("b")), history.S(b("c"))}, nil, -1},
		{"旧历史前缀在Branch处耗尽改走新分支", v2,
			[]history.Event{history.S(b("a"))}, 1, 3,
			[]history.Event{history.S(b("a")), history.M(b("p")), history.S(b("b2")), history.S(b("c"))}, nil, -1},
		{"已打补丁历史续跑一步", v2,
			[]history.Event{history.S(b("a")), history.M(b("p")), history.S(b("b2"))}, 3, 1,
			[]history.Event{history.S(b("a")), history.M(b("p")), history.S(b("b2")), history.S(b("c"))}, nil, -1},
		{"步骤名不符", code.Code{st("a"), st("x"), st("c")},
			[]history.Event{history.S(b("a")), history.S(b("b")), history.S(b("c"))},
			0, 0, nil, ErrMismatch, 1},
		{"历史有多余事件", code.Code{st("a")},
			[]history.Event{history.S(b("a")), history.S(b("b"))},
			0, 0, nil, ErrHistoryExtra, 1},
		{"步骤处遇标记", code.Code{st("a"), st("b")},
			[]history.Event{history.S(b("a")), history.M(b("p")), history.S(b("b"))},
			0, 0, nil, ErrUnexpectedMarker, 1},
		{"重复pid只写一次标记", code.Code{
			code.Branch{Pid: b("p"), New: []code.Item{st("b2")}, Old: []code.Item{st("b")}},
			code.Branch{Pid: b("p"), New: []code.Item{st("d2")}, Old: []code.Item{st("d")}}},
			nil, 0, 3,
			[]history.Event{history.M(b("p")), history.S(b("b2")), history.S(b("d2"))}, nil, -1},
		{"旧分支遇空O", code.Code{st("a"),
			code.Branch{Pid: b("p"), New: []code.Item{st("b2")}}, st("c")},
			[]history.Event{history.S(b("a")), history.S(b("c"))}, 2, 0,
			[]history.Event{history.S(b("a")), history.S(b("c"))}, nil, -1},
	}
	for _, tc := range cases {
		res, final, err := seedAndRun(t, tc.hist, tc.code)
		switch {
		case tc.wantErr == nil:
			if err != nil {
				t.Fatalf("%s: 意外错误 %v", tc.name, err)
			}
			if res.Consumed != tc.wantConsumed || res.Continued != tc.wantContinued {
				t.Fatalf("%s: got (%d,%d), want (%d,%d)",
					tc.name, res.Consumed, res.Continued, tc.wantConsumed, tc.wantContinued)
			}
			if !evsEqual(final, tc.wantFinal) {
				t.Fatalf("%s: final=%v, want %v", tc.name, final, tc.wantFinal)
			}
		default:
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("%s: got %v, want errors.Is %v", tc.name, err, tc.wantErr)
			}
			var idx int
			switch e := err.(type) {
			case *MismatchError:
				idx = e.Index
			case *UnexpectedMarkerError:
				idx = e.Index
			case *HistoryExtraError:
				idx = e.Index
			}
			if idx != tc.wantErrIndex {
				t.Fatalf("%s: 错误下标=%d, want %d", tc.name, idx, tc.wantErrIndex)
			}
			if final != nil && !evsEqual(final, tc.hist) {
				t.Fatalf("%s: 出错的 Run 改了历史: %v", tc.name, final)
			}
		}
		t.Logf("%s: 输入 历史=%v → 输出 res=%+v err=%v 最终历史=%v（判定依据：游标/标记规则）",
			tc.name, tc.hist, res, err, final)
	}
}

// TestPrefixProperty 对当前代码从空历史续跑产生的完整历史 H，
// 遍历所有崩溃点 k，H[:k] 再 Run 后历史必须等于 H。
func TestPrefixProperty(t *testing.T) {
	c := code.Code{st("a"),
		code.Branch{Pid: b("p1"), New: []code.Item{st("n1")}, Old: []code.Item{st("o1")}},
		st("m"),
		code.Branch{Pid: b("p2"), New: []code.Item{st("n2"), st("n3")}, Old: nil},
		st("z")}
	store := history.NewStore()
	wf := b("wf")
	if _, err := Run(store, wf, c); err != nil {
		t.Fatalf("full run: %v", err)
	}
	full := store.Snapshot(wf)
	for k := 0; k <= len(full); k++ {
		s2 := history.NewStore()
		if k > 0 {
			if err := s2.Append(wf, 0, full[:k]); err != nil {
				t.Fatal(err)
			}
		}
		res, err := Run(s2, wf, c)
		if err != nil {
			t.Fatalf("k=%d: %v", k, err)
		}
		if got := s2.Snapshot(wf); !evsEqual(got, full) {
			t.Fatalf("k=%d: final=%v, want %v", k, got, full)
		}
		t.Logf("崩溃点 k=%d: 消费=%d 续跑=%d → 历史复原为 H（依据：前缀性质）",
			k, res.Consumed, res.Continued)
	}
}

// TestOldHistoryOnUpgradedCode 旧代码历史在升级代码上重放：
// 完整旧历史消费殆尽，旧前缀则按新分支续跑。
func TestOldHistoryOnUpgradedCode(t *testing.T) {
	v1 := code.Code{st("a"), st("b"), st("c")}
	v2 := code.Code{st("a"),
		code.Branch{Pid: b("p"), New: []code.Item{st("b2")}, Old: []code.Item{st("b")}},
		st("c")}
	store := history.NewStore()
	wf := b("wf")
	if _, err := Run(store, wf, v1); err != nil {
		t.Fatal(err)
	}
	oldFull := store.Snapshot(wf)
	res, final, err := seedAndRun(t, oldFull, v2)
	if err != nil || res.Consumed != 3 || res.Continued != 0 {
		t.Fatalf("旧完整历史重放: res=%+v err=%v", res, err)
	}
	if !evsEqual(final, oldFull) {
		t.Fatalf("旧历史被改写: %v", final)
	}
	t.Logf("旧历史 %v 在升级代码上重放 → %+v，历史不变（依据：无标记走 O 分支）", oldFull, res)
}
