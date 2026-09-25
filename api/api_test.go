package api_test

import (
	"errors"
	"maps"
	"sync"
	"testing"

	"ontology/api"
)

// setup 建立第三节状态：R0/R1/R2 的 applied = 5/3/2，k 值 = E/C/B。Write 的 key 非空，err 恒 nil。
func setup() *api.API {
	a := api.New()
	a.Write("k", "A")
	a.Write("k", "B")
	a.Offline(2)
	a.Write("k", "C")
	a.Offline(1)
	a.Write("k", "D")
	a.Write("k", "E")
	a.Online(1)
	a.Online(2)
	return a
}

// TestMonotonicReads 钉住不变量1：第三节五行操作，lsn 非递减且返回值正确。
func TestMonotonicReads(t *testing.T) {
	a := setup()
	s, _ := a.Open(0)
	steps := []struct{ sw, wantL int }{{-1, 5}, {1, 0}, {-1, 5}, {2, 0}, {-1, 5}} // sw>=0 为切换目标，-1 为 Read
	prev := 0
	for i, st := range steps {
		if st.sw >= 0 {
			if err := a.SwitchReplica(s, st.sw); err != nil {
				t.Fatalf("step %d: %v", i, err)
			}
			continue
		}
		v, l, err := a.Read(s, "k")
		if err != nil || v != "E" || l != st.wantL || l < prev {
			t.Fatalf("step %d: got (%q,%d,%v), prev=%d", i, v, l, err, prev)
		}
		prev = l
	}
}

func TestNaiveReference(t *testing.T) {
	cases := [][]string{{"x:1"}, {"x:1", "y:2", "x:3"}, {"a:1", "b:2", "c:3", "a:4", "b:5", "c:6"}}
	for _, tc := range cases {
		a := api.New()
		want := map[string]string{}
		for _, kv := range tc {
			a.Write(kv[:1], kv[2:])
			want[kv[:1]] = kv[2:]
		}
		s, _ := a.Open(0)
		for k, wv := range want {
			if v, _, err := a.Read(s, k); err != nil || v != wv {
				t.Fatalf("%v: Read(%q)=(%q,%v), want %q", tc, k, v, err, wv)
			}
		}
		if got := a.View(); !maps.Equal(got, want) {
			t.Fatalf("%v: View=%v, want %v", tc, got, want)
		}
	}
}

// TestSwitchCaughtUp 钉住不变量3：切换成功后，新副本读到的 lsn >= 会话已见 seen。
func TestSwitchCaughtUp(t *testing.T) {
	a := setup()
	s, _ := a.Open(0)
	_, seen, _ := a.Read(s, "k")
	for _, idx := range []int{1, 2} {
		if err := a.SwitchReplica(s, idx); err != nil {
			t.Fatal(err)
		}
		if _, l, err := a.Read(s, "k"); err != nil || l < seen {
			t.Fatalf("切到 R%d 后 lsn=%d < seen=%d", idx, l, seen)
		}
	}
}

// TestFailureNoTrace 钉住不变量4：三类错误互不相同，被拒后状态、日志、会话不变。
func TestFailureNoTrace(t *testing.T) {
	a := setup()
	s, _ := a.Open(0)
	a.Read(s, "k")
	before := a.View()
	a.Offline(1)
	cases := []struct {
		err  error
		want error
	}{
		{func() error { _, e := a.Write("", "v"); return e }(), api.ErrEmptyKey},
		{func() error { _, _, e := a.Read(s, ""); return e }(), api.ErrEmptyKey},
		{a.Offline(7), api.ErrBadIndex},
		{a.Online(-1), api.ErrBadIndex},
		{a.SwitchReplica(s, 9), api.ErrBadIndex},
		{a.SwitchReplica(s, 1), api.ErrOffline}, // R1 仍 Offline
	}
	for i, c := range cases {
		if !errors.Is(c.err, c.want) {
			t.Fatalf("case %d: got %v, want %v", i, c.err, c.want)
		}
	}
	if errors.Is(api.ErrEmptyKey, api.ErrBadIndex) || errors.Is(api.ErrBadIndex, api.ErrOffline) ||
		errors.Is(api.ErrEmptyKey, api.ErrOffline) {
		t.Fatal("哨兵错误不互异")
	}
	a.Online(1)
	if got := a.View(); !maps.Equal(got, before) {
		t.Fatalf("拒绝后 View 改变: %v -> %v", before, got)
	}
	if v, l, _ := a.Read(s, "k"); v != "E" || l != 5 {
		t.Fatalf("拒绝后会话读改变: (%q,%d)", v, l)
	}
	if _, err := a.Write("k", "F"); err != nil { // 之后仍可正常使用
		t.Fatal(err)
	}
	if v, _, _ := a.Read(s, "k"); v != "F" {
		t.Fatalf("拒绝后无法继续: %q", v)
	}
}

func TestConcurrentReads(t *testing.T) {
	a := setup()
	s, _ := a.Open(0)
	wantV, wantL, _ := a.Read(s, "k")
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 50; j++ {
				v, l, err := a.Read(s, "k")
				if err != nil || v != wantV || l != wantL {
					t.Errorf("got (%q,%d,%v), want (%q,%d)", v, l, err, wantV, wantL)
					return
				}
				a.View()
				a.SelfCheck()
			}
		}()
	}
	close(start)
	wg.Wait()
}
