package apply

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ontology/name"
	"ontology/plan"
)

func TestRunFailureRollback(t *testing.T) {
	reqs := []plan.Request{{Old: "a", New: "b"}, {Old: "b", New: "c"}, {Old: "x", New: "y"}}
	cases := []struct {
		name string
		k    int
	}{
		{"第一步", 1},
		{"中间步", 2},
		{"最后一步", 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ns := name.New("a", "b", "x")
			before := ns.Snapshot()
			ex := &Executor{NS: ns, FailBefore: tc.k}
			_, err := ex.Run(reqs)
			if !errors.Is(err, ErrInjected) {
				t.Fatalf("err=%v, want ErrInjected", err)
			}
			if !name.Equal(before, ns.Snapshot()) {
				t.Fatal("回滚后命名空间与执行前不一致")
			}
		})
	}
}

func TestRunOutcomes(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name      string
		init      []string
		reqs      []plan.Request
		wantErr   error
		wantSteps int
	}{
		{"空请求", []string{"a"}, nil, nil, 0},
		{"单请求", []string{"a"}, []plan.Request{{Old: "a", New: "b"}}, nil, 1},
		{"自环无操作", []string{"a"}, []plan.Request{{Old: "a", New: "a"}}, nil, 0},
		{"链执行", []string{"a", "b"}, []plan.Request{{Old: "a", New: "b"}, {Old: "b", New: "c"}}, nil, 2},
		{"冲突拒绝", []string{"a", "x"}, []plan.Request{{Old: "a", New: "x"}}, plan.ErrTargetExists, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ns := name.New(tc.init...)
			before := ns.Snapshot()
			log := filepath.Join(dir, tc.name+".log")
			ex := &Executor{NS: ns, LogPath: log}
			p, err := ex.Run(tc.reqs)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v, want %v", err, tc.wantErr)
			}
			if tc.wantErr != nil {
				if !name.Equal(before, ns.Snapshot()) {
					t.Fatal("冲突后命名空间被修改")
				}
				return
			}
			if len(p.Steps) != tc.wantSteps {
				t.Fatalf("步数=%d, want %d", len(p.Steps), tc.wantSteps)
			}
			data, err := os.ReadFile(log)
			if err != nil || len(data) < HeaderLen+FooterLen || string(data[:8]) != HeaderMagic {
				t.Fatalf("日志无效: len=%d err=%v", len(data), err)
			}
		})
	}
}

func TestRunFinalState(t *testing.T) {
	ns := name.New("a", "b", "c")
	ex := &Executor{NS: ns}
	_, err := ex.Run([]plan.Request{{Old: "a", New: "b"}, {Old: "b", New: "c"}, {Old: "c", New: "a"}})
	if err != nil {
		t.Fatal(err)
	}
	got := ns.Snapshot()
	if !name.Equal(got, map[string]bool{"a": true, "b": true, "c": true}) {
		t.Fatalf("三元环执行后状态错误: %v", got)
	}
}

func TestRunBlocksConcurrentModification(t *testing.T) {
	ns := name.New("a", "b")
	started := make(chan struct{})
	ex := &Executor{
		NS: ns,
		OnStep: func(i int) {
			if i == 0 {
				close(started)
				time.Sleep(50 * time.Millisecond)
			}
		},
	}
	done := make(chan struct{})
	go func() {
		ex.Run([]plan.Request{{Old: "a", New: "c"}, {Old: "b", New: "d"}})
		close(done)
	}()
	<-started
	acquired := make(chan struct{})
	go func() {
		ns.Lock()
		ns.Unlock()
		close(acquired)
	}()
	select {
	case <-acquired:
		t.Fatal("批量执行期间并发修改未被阻塞")
	case <-time.After(10 * time.Millisecond):
	}
	<-done
	<-acquired
}
