package ontology

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

func TestSlogAndNopLogger(t *testing.T) {
	var buf bytes.Buffer
	l := SlogLogger{Logger: slog.New(slog.NewTextHandler(&buf, nil)), Level: slog.LevelInfo}
	l.Logf(context.Background(), "hello %s", "world")
	if !strings.Contains(buf.String(), "hello world") {
		t.Fatalf("slog output missing message: %q", buf.String())
	}
	NopLogger().Logf(context.Background(), "must not panic: %d", 1)

	var nilTarget *Error
	if nilTarget.Error() != "" {
		t.Fatal("nil Error must render empty string")
	}
}

func TestOptimisticRecomputeUnderContention(t *testing.T) {
	base := []Entry{{Key: "a", Value: "0"}}
	m, err := NewManager(base, Config{})
	if err != nil {
		t.Fatal(err)
	}

	// 制造“读锁下算出的基线版本在提交前已被推进”的情形：
	// 用钩子无法注入，改为高并发提交不同目标快照，最终状态必须等于
	// 最后获胜者之一且所有统计自洽；重复运行配合 -race 覆盖重算分支。
	var wg sync.WaitGroup
	for n := 0; n < 100; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			v := string(rune('a' + n%26))
			_, _, _ = m.DiffAndApply(context.Background(), []Entry{{Key: v, Value: "x"}})
		}(n)
	}
	wg.Wait()
	if err := m.SelfCheck(); err != nil {
		t.Fatalf("self check after contention: %v", err)
	}
	snap := m.Snapshot()
	if err := Validate(snap); err != nil {
		t.Fatalf("final snapshot invalid: %v", err)
	}
	st := m.Stats()
	if st.ChangesTotal != st.Inserts+st.Deletes+st.Updates {
		t.Fatalf("stats invariant broken: %+v", st)
	}
	if st.Commits != m.Version() {
		t.Fatalf("commits %d != version %d", st.Commits, m.Version())
	}
}
