package commit

import (
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
)

// TestRejectsNoTrace：四类可判定错误互不相同，被拒后不留痕迹，之后仍可用。
func TestRejectsNoTrace(t *testing.T) {
	if _, err := New(0); !errors.Is(err, ErrBadConfig) {
		t.Fatalf("New(0) = %v, want ErrBadConfig", err)
	}
	cases := []struct {
		name  string
		batch []Op
		want  error
	}{
		{"nil batch", nil, ErrEmptyBatch},
		{"empty batch", []Op{}, ErrEmptyBatch},
		{"zero net drops all", []Op{{"a", 1}, {"a", -1}}, ErrEmptyBatch},
		{"empty key", []Op{{"", 1}}, ErrEmptyKey},
		{"empty key later", []Op{{"a", 1}, {"", 2}}, ErrEmptyKey},
		{"zero delta", []Op{{"a", 0}}, ErrZeroDelta},
	}
	for _, tc := range cases {
		c, _ := New(3)
		if err := c.Commit(tc.batch); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.want)
		}
		if v := c.View(); len(v) != 0 || c.Retries() != 0 {
			t.Errorf("%s: rejected commit left a trace: %v retries=%d", tc.name, v, c.Retries())
		}
		if err := c.Commit([]Op{{"ok", 1}}); err != nil || c.View()["ok"] != 1 {
			t.Errorf("%s: engine unusable after reject: %v", tc.name, err)
		}
	}
	all := []error{ErrEmptyBatch, ErrEmptyKey, ErrZeroDelta, ErrBadConfig, ErrContended}
	for i := range all {
		for j := i + 1; j < len(all); j++ {
			if all[i] == all[j] {
				t.Fatalf("sentinels %d and %d identical", i, j)
			}
		}
	}
}

// stallSnapshot 让第一个到达 snapshot 阶段的提交者阻塞，直到 release 关闭。
func stallSnapshot() (snap <-chan struct{}, release chan<- struct{}) {
	s, r := make(chan struct{}), make(chan struct{})
	var blocked int32
	Hook = func(phase string, keys []string) {
		if phase == "snapshot" && atomic.CompareAndSwapInt32(&blocked, 0, 1) {
			close(s)
			<-r
		}
	}
	return s, r
}

// TestVersionPlusOne：成功提交使每个 Key 版本恰 +1；冲突回滚不抬版本。
func TestVersionPlusOne(t *testing.T) {
	c, _ := New(4)
	for _, b := range [][]Op{{{"a", 1}, {"b", 2}}, {{"a", 5}}} {
		if err := c.Commit(b); err != nil {
			t.Fatal(err)
		}
	}
	if c.cells["a"].Snapshot().Ver != 2 || c.cells["b"].Snapshot().Ver != 1 {
		t.Fatalf("vers = a:%d b:%d, want 2/1",
			c.cells["a"].Snapshot().Ver, c.cells["b"].Snapshot().Ver)
	}
	// 强制一次冲突：被阻塞者快照后，另一提交者先成功。
	c2, _ := New(4)
	snap, release := stallSnapshot()
	defer func() { Hook = nil }()
	done := make(chan error, 1)
	go func() { done <- c2.Commit([]Op{{"k", 1}}) }()
	<-snap
	if err := c2.Commit([]Op{{"k", 10}}); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := c2.View()["k"]; got != 11 {
		t.Fatalf("k = %d, want 11", got)
	}
	if v := c2.cells["k"].Snapshot().Ver; v != 2 {
		t.Fatalf("ver = %d, want 2 (conflict must not bump version)", v)
	}
	if c2.Retries() != 1 {
		t.Fatalf("retries = %d, want 1", c2.Retries())
	}
}

// TestContended：maxRetries=1 时冲突方返回 ErrContended 且不留任何痕迹。
func TestContended(t *testing.T) {
	c, _ := New(1)
	snap, release := stallSnapshot()
	defer func() { Hook = nil }()
	done := make(chan error, 1)
	go func() { done <- c.Commit([]Op{{"a", 1}}) }()
	<-snap
	if err := c.Commit([]Op{{"a", 10}}); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; !errors.Is(err, ErrContended) {
		t.Fatalf("got %v, want ErrContended", err)
	}
	if got := c.View()["a"]; got != 10 || c.Retries() != 0 || c.cells["a"].Snapshot().Ver != 1 {
		t.Fatalf("contended commit left a trace: a=%d retries=%d ver=%d",
			got, c.Retries(), c.cells["a"].Snapshot().Ver)
	}
}

// TestCheckCountBounded：单 Key 事务的校验个数不随视图规模 m 增长。
func TestCheckCountBounded(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		c, _ := New(2)
		for i := 0; i < m; i++ {
			if err := c.Commit([]Op{{fmt.Sprintf("k%05d", i), 1}}); err != nil {
				t.Fatal(err)
			}
		}
		if err := c.Commit([]Op{{"k00000", 1}}); err != nil {
			t.Fatal(err)
		}
		if c.lastChecks != 1 {
			t.Fatalf("m=%d: lastChecks = %d, want 1 (hash lookup, no full scan)", m, c.lastChecks)
		}
		if err := c.Commit([]Op{{"k00001", 1}, {"k00002", 1}, {"k00003", 1}}); err != nil {
			t.Fatal(err)
		}
		if c.lastChecks != 3 {
			t.Fatalf("m=%d: lastChecks = %d, want 3 (= deduped keys)", m, c.lastChecks)
		}
	}
}
