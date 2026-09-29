package dimcache

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// testLogger 把每一步输入、栅栏、缓存与判定依据打印到测试日志，
// 同时在内存保留一份，便于断言“拒绝不留痕”。
type testLogger struct {
	t  *testing.T
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *testLogger) Log(step string, fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	line := fmt.Sprintf("  [%s] %v\n", step, fields)
	l.b.WriteString(line)
	l.t.Log(strings.TrimRight(line, "\n"))
}

func (l *testLogger) text() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func newSystem(t *testing.T, maxKeys int) (*Cache, *Source, *EventQueue) {
	logger := &testLogger{t: t}
	src := NewSource(logger)
	q := NewEventQueue(logger)
	c := NewCache(src, q, maxKeys, logger)
	return c, src, q
}

// assertEntryMatchesSource 校验某键缓存与无缓存直接读源头一致。
func assertEntryMatchesSource(t *testing.T, c *Cache, src *Source, key string) {
	t.Helper()
	got, inCache := c.SnapshotEntry(key)
	rec, found := src.Read(context.Background(), key)
	switch {
	case !found:
		// 源头从无此行：缓存可以没有条目；若有，必须是版本 0 的负缓存。
		if inCache && (!got.Negative || got.Version != 0) {
			t.Fatalf("key %q: expected absent/negative-v0, got %+v", key, got)
		}
	case rec.Deleted:
		if !inCache || !got.Negative || got.Version != rec.Version {
			t.Fatalf("key %q: expected negative v%d, got %+v(inCache=%v)", key, rec.Version, got, inCache)
		}
	default:
		if !inCache || got.Negative || got.Value != rec.Value || got.Version != rec.Version {
			t.Fatalf("key %q: expected value %q v%d, got %+v(inCache=%v)", key, rec.Value, rec.Version, got, inCache)
		}
	}
}

func mustToken(t *testing.T, tok *Token, err error) *Token {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected issue error: %v", err)
	}
	return tok
}

func issue(t *testing.T, c *Cache, key string) *Token {
	t.Helper()
	tok, err := c.IssueReadToken(context.Background(), key)
	return mustToken(t, tok, err)
}

func wantErr(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("want error %v, got %v", target, err)
	}
}

// fmtState 输出源头、事件队列、缓存、栅栏、令牌与读源头计数的完整快照，
// 用于断言“拒绝不留痕”。
func fmtState(src *Source, q *EventQueue, c *Cache) string {
	src.mu.Lock()
	rows := fmt.Sprintf("%v", src.rows)
	reads := src.reads
	src.mu.Unlock()
	c.mu.RLock()
	state := fmt.Sprintf("rows=%s reads=%d queue=%d fences=%v entries=%v tokens=%d tracked=%v",
		rows, reads, q.Len(), c.fences, c.entries, len(c.tokens), c.tracked)
	c.mu.RUnlock()
	return state
}

// stateDigest 给出栅栏与缓存条目的确定性摘要，用于复现性对比。
func (c *Cache) stateDigest() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return fmt.Sprintf("fences=%v entries=%v", c.fences, c.entries)
}
