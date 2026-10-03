package replay

import (
	"errors"
	"testing"

	"ontology/code"
	"ontology/history"
)

func TestNondeterministicErrorsAndIndices(t *testing.T) {
	cases := []struct {
		name string
		hist []history.Event
		prog code.Code
		want error
		idx  int
	}{
		{
			name: "mismatch at 1",
			hist: []history.Event{
				history.Step([]byte("a")), history.Step([]byte("b")), history.Step([]byte("c")),
			},
			prog: code.Code{
				code.Step([]byte("a")), code.Step([]byte("x")), code.Step([]byte("c")),
			},
			want: ErrMismatch, idx: 1,
		},
		{
			name: "history extra at 1",
			hist: []history.Event{history.Step([]byte("a")), history.Step([]byte("b"))},
			prog: code.Code{code.Step([]byte("a"))},
			want: ErrHistoryExtra, idx: 1,
		},
		{
			name: "unexpected marker at 1 from step",
			hist: []history.Event{
				history.Step([]byte("a")), history.Marker([]byte("p")), history.Step([]byte("b")),
			},
			prog: code.Code{code.Step([]byte("a")), code.Step([]byte("b"))},
			want: ErrUnexpectedMarker, idx: 1,
		},
		{
			name: "unexpected marker at branch with other pid",
			hist: []history.Event{history.Marker([]byte("q"))},
			prog: code.Code{
				code.Branch([]byte("p"), []code.Item{code.Step([]byte("n"))}, []code.Item{code.Step([]byte("o"))}),
			},
			want: ErrUnexpectedMarker, idx: 0,
		},
		{
			name: "mismatch inside old branch uses actual c",
			hist: []history.Event{
				history.Step([]byte("a")), history.Step([]byte("x")),
			},
			prog: v2(),
			want: ErrMismatch, idx: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, wf := seed(t, tc.name, tc.hist)
			r := NewRunner(store)
			t.Logf("input : code=%s history=%s", codeString(tc.prog), evsString(tc.hist))
			_, _, err := r.Run(wf, tc.prog)
			t.Logf("output: err=%v; basis=%s", err, tc.name)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			idx, ok := IndexFrom(err)
			if !ok || idx != tc.idx {
				t.Fatalf("index = (%d,%v), want %d,true", idx, ok, tc.idx)
			}
			mustEqualEvents(t, store.Snapshot(wf), tc.hist, "history unchanged on error")
		})
	}
}

func TestEmptyOBranch(t *testing.T) {
	// 旧历史 S(a) 后遇到 Branch（下一事件是 S(c)），走空 O：不产生也不消费
	// 该位置的 Step，随后 c 与 Step(c) 继续比对。
	prog := code.Code{
		code.Step([]byte("a")),
		code.Branch([]byte("p"), []code.Item{code.Step([]byte("b2"))}, nil),
		code.Step([]byte("c")),
	}
	hist := []history.Event{history.Step([]byte("a")), history.Step([]byte("c"))}
	store, wf := seed(t, "empty-o", hist)
	r := NewRunner(store)
	consumed, cont, err := r.Run(wf, prog)
	t.Logf("input code=%s hist=%s; output consumed=%d cont=%d err=%v",
		codeString(prog), evsString(hist), consumed, cont, err)
	if err != nil || consumed != 2 || cont != 0 {
		t.Fatalf("got (%d,%d,%v), want (2,0,nil)", consumed, cont, err)
	}

	// 空历史续跑：走 N，生成 M(p), S(b2), S(c)。
	store2, wf2 := seed(t, "empty-o-cont", nil)
	r2 := NewRunner(store2)
	_, _, err = r2.Run(wf2, prog)
	if err != nil {
		t.Fatalf("continuation: %v", err)
	}
	mustEqualEvents(t, store2.Snapshot(wf2), []history.Event{
		history.Step([]byte("a")), history.Marker([]byte("p")),
		history.Step([]byte("b2")), history.Step([]byte("c")),
	}, "empty O continuation")
}

func TestRejectionPriority(t *testing.T) {
	store := &history.Store{}
	r := NewRunner(store)

	// 参数非法优先于一切（wf 为空）。
	if _, _, err := r.Run(nil, code.Code{code.Step([]byte("a"))}); !errors.Is(err, ErrArgument) {
		t.Fatalf("empty wf: %v", err)
	}

	// 非法代码结构 ErrCode 优先于执行期错误：即使历史也对不上。
	bad := code.Code{
		code.Branch([]byte("p"), []code.Item{code.Branch([]byte("q"), nil, nil)}, nil),
	}
	if _, _, err := r.Run([]byte("w"), bad); !errors.Is(err, code.ErrCode) {
		t.Fatalf("nested code: %v", err)
	}

	// 空程序属参数非法（code 包先报告）。
	if _, _, err := r.Run([]byte("w"), code.Code{}); !errors.Is(err, code.ErrArgument) {
		t.Fatalf("empty code: %v", err)
	}

	// 追加期 ErrConflict：快照长度 0，另一运行先写入后 expect 失配。
	wf := []byte("conflict")
	prog := code.Code{code.Step([]byte("a")), code.Step([]byte("b"))}
	if _, _, err := r.Run(wf, prog); err != nil {
		t.Fatalf("first run: %v", err)
	}
	staleSnap := []history.Event{}
	sim := newSimulator(staleSnap)
	res, err := sim.run(prog)
	if err != nil {
		t.Fatalf("sim: %v", err)
	}
	if err := store.Append(wf, len(staleSnap), res.newEvents); !errors.Is(err, history.ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
}
