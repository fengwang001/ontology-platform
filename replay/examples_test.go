package replay

import (
	"errors"
	"testing"

	"ontology/code"
	"ontology/history"
)

// v2 = [Step a, Branch(p, [Step b2], [Step b]), Step c]
func v2() code.Code {
	return code.Code{
		code.Step([]byte("a")),
		code.Branch([]byte("p"),
			[]code.Item{code.Step([]byte("b2"))},
			[]code.Item{code.Step([]byte("b"))}),
		code.Step([]byte("c")),
	}
}

func TestSpecExamples(t *testing.T) {
	type tc struct {
		name     string
		hist     []history.Event
		consumed int
		cont     int
		final    []history.Event
	}
	cases := []tc{
		{
			name:     "empty history -> full continuation through new branch",
			hist:     nil,
			consumed: 0,
			cont:     4,
			final: []history.Event{
				history.Step([]byte("a")), history.Marker([]byte("p")),
				history.Step([]byte("b2")), history.Step([]byte("c")),
			},
		},
		{
			name: "old-code history replays through old branch",
			hist: []history.Event{
				history.Step([]byte("a")), history.Step([]byte("b")), history.Step([]byte("c")),
			},
			consumed: 3,
			cont:     0,
		},
		{
			name:     "old prefix cut before branch -> continuation takes new branch",
			hist:     []history.Event{history.Step([]byte("a"))},
			consumed: 1,
			cont:     3,
			final: []history.Event{
				history.Step([]byte("a")), history.Marker([]byte("p")),
				history.Step([]byte("b2")), history.Step([]byte("c")),
			},
		},
		{
			name: "patched prefix consumes 3 and continues with c",
			hist: []history.Event{
				history.Step([]byte("a")), history.Marker([]byte("p")),
				history.Step([]byte("b2")),
			},
			consumed: 3,
			cont:     1,
			final: []history.Event{
				history.Step([]byte("a")), history.Marker([]byte("p")),
				history.Step([]byte("b2")), history.Step([]byte("c")),
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store, wf := seed(t, c.name, c.hist)
			r := NewRunner(store)
			t.Logf("input  : wf=%q code=%s history=%s", c.name, codeString(v2()), evsString(c.hist))
			consumed, cont, err := r.Run(wf, v2())
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			t.Logf("output : consumed=%d continued=%d final=%s; basis=%s",
				consumed, cont, evsString(store.Snapshot(wf)), c.name)
			if consumed != c.consumed || cont != c.cont {
				t.Fatalf("counts = (%d,%d), want (%d,%d)", consumed, cont, c.consumed, c.cont)
			}
			final := c.final
			if final == nil {
				final = c.hist
			}
			mustEqualEvents(t, store.Snapshot(wf), final, "final history")
		})
	}
}

func TestRepeatPidWritesMarkerOnce(t *testing.T) {
	// [Branch(p,[b2],[b]), Branch(p,[d2],[d])] 对空历史：
	// 第二个 Branch 因 p∈P 不再写标记，也不消费历史。
	program := code.Code{
		code.Branch([]byte("p"), []code.Item{code.Step([]byte("b2"))}, []code.Item{code.Step([]byte("b"))}),
		code.Branch([]byte("p"), []code.Item{code.Step([]byte("d2"))}, []code.Item{code.Step([]byte("d"))}),
	}
	store, wf := seed(t, "repeat-pid", nil)
	r := NewRunner(store)
	consumed, cont, err := r.Run(wf, program)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []history.Event{
		history.Marker([]byte("p")), history.Step([]byte("b2")), history.Step([]byte("d2")),
	}
	t.Logf("input code=%s; output consumed=%d cont=%d final=%s",
		codeString(program), consumed, cont, evsString(store.Snapshot(wf)))
	if consumed != 0 || cont != 3 {
		t.Fatalf("counts = (%d,%d), want (0,3)", consumed, cont)
	}
	mustEqualEvents(t, store.Snapshot(wf), want, "repeat pid")

	// 重放完整历史：第二个 Branch 看到 S(d2) 但 P 已有 p，仍走 N，消费全部。
	consumed, cont, err = r.Run(wf, program)
	if err != nil || consumed != 3 || cont != 0 {
		t.Fatalf("replay full: consumed=%d cont=%d err=%v", consumed, cont, err)
	}
}

func TestRejectionDoesNotAppend(t *testing.T) {
	// 非确定性错误后历史保持原样。
	store, wf := seed(t, "rej",
		[]history.Event{history.Step([]byte("a")), history.Marker([]byte("p")), history.Step([]byte("b"))})
	r := NewRunner(store)
	program := code.Code{
		code.Step([]byte("a")), code.Step([]byte("b")),
	}
	_, _, err := r.Run(wf, program)
	if !errors.Is(err, ErrUnexpectedMarker) {
		t.Fatalf("want ErrUnexpectedMarker, got %v", err)
	}
	if i, ok := IndexFrom(err); !ok || i != 1 {
		t.Fatalf("index = (%d,%v), want 1,true", i, ok)
	}
	if len(store.Snapshot(wf)) != 3 {
		t.Fatalf("failed run must not change history")
	}
}
