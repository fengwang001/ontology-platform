package ontology

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

// 游标不透明：不得直接暴露任何主键明文。
func TestCursorIsOpaque(t *testing.T) {
	st := seededStore(3)
	p, _ := st.Scan("", 2)
	for _, key := range keysOf(p) {
		if strings.Contains(p.NextCursor, key) {
			t.Fatalf("cursor leaks key %q: %s", key, p.NextCursor)
		}
	}
	if decoded, err := base64.RawURLEncoding.DecodeString(p.NextCursor); err == nil {
		for _, key := range keysOf(p) {
			if strings.Contains(string(decoded), key) {
				t.Fatalf("decoded cursor leaks key %q", key)
			}
		}
	}
}

// 伪造、截断、篡改游标均为 ErrCursorMalformed，绝不静默从头开始或 panic。
func TestCursorTamperDetection(t *testing.T) {
	st := seededStore(3)
	p, _ := st.Scan("", 2)
	c := p.NextCursor

	cases := map[string]string{
		"garbage":     "not-a-cursor!!!",
		"empty":       "",
		"truncated":   c[:len(c)-4],
		"prefix":      c[:8],
		"tampered":    flipLast(c),
		"foreign b64": base64.RawURLEncoding.EncodeToString(make([]byte, cursorTotal)),
	}
	for name, cur := range cases {
		if cur == "" {
			// 空串表示“开启新遍历”，不属于篡改，跳过。
			continue
		}
		if _, err := st.Scan(cur, 2); !errors.Is(err, ErrCursorMalformed) {
			t.Fatalf("%s: expected ErrCursorMalformed, got %v", name, err)
		}
		if _, _, err := st.Stats(cur); !errors.Is(err, ErrCursorMalformed) {
			t.Fatalf("%s stats: expected ErrCursorMalformed, got %v", name, err)
		}
	}
}

func flipLast(s string) string {
	b := []byte(s)
	switch b[len(b)-1] {
	case 'A':
		b[len(b)-1] = 'B'
	default:
		b[len(b)-1] = 'A'
	}
	return string(b)
}

// 跨会话使用已失效会话的游标：ErrCursorInvalidated，且与格式非法不同。
func TestCrossSessionInvalidatedCursor(t *testing.T) {
	st := seededStore(5)
	p, _ := st.Scan("", 2)
	cursor := p.NextCursor

	// 失效前游标可用。
	if _, err := st.Scan(cursor, 2); err != nil {
		t.Fatalf("cursor should work before invalidation: %v", err)
	}
	if err := st.Invalidate(cursor); err != nil {
		t.Fatal(err)
	}

	_, err := st.Scan(cursor, 2)
	if !errors.Is(err, ErrCursorInvalidated) {
		t.Fatalf("expected ErrCursorInvalidated, got %v", err)
	}
	if errors.Is(err, ErrCursorMalformed) {
		t.Fatal("invalidated-session error must differ from malformed error")
	}
	if _, _, err := st.Stats(cursor); !errors.Is(err, ErrCursorInvalidated) {
		t.Fatalf("stats expected ErrCursorInvalidated, got %v", err)
	}

	// 另一个仍然有效的会话游标不受影响。
	other, _ := st.Scan("", 2)
	if _, err := st.Scan(other.NextCursor, 2); err != nil {
		t.Fatalf("unrelated session must keep working: %v", err)
	}

	// 重复失效返回 ErrCursorInvalidated（幂等）。
	if err := st.Invalidate(cursor); !errors.Is(err, ErrCursorInvalidated) {
		t.Fatalf("re-invalidate expected ErrCursorInvalidated, got %v", err)
	}
}

// 不同 Store（不同密钥）伪造出结构合法的游标也必须被拒绝。
func TestCursorFromForeignStoreRejected(t *testing.T) {
	st1 := seededStore(3)
	st2 := seededStore(3)
	p, _ := st1.Scan("", 2)
	if _, err := st2.Scan(p.NextCursor, 2); !errors.Is(err, ErrCursorMalformed) {
		t.Fatalf("foreign-store cursor expected ErrCursorMalformed, got %v", err)
	}
}
