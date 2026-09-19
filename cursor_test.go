package ontology

import (
	"errors"
	"strings"
	"testing"
)

// TestCursorTampering 伪造、截断、篡改均返回 ErrInvalidCursor。
func TestCursorTampering(t *testing.T) {
	s := seedStore(4)
	p, _ := s.Scan("", 2)
	cur := p.Cursor

	bad := []string{
		"",
		"not-a-real-cursor",
		cur[:len(cur)-1],        // 截断
		cur[1:],                 // 头部截断
		cur[:4] + "A" + cur[5:], // 中间篡改
		strings.Repeat("A", 40), // 伪造
		strings.ToUpper(cur),    // base64 字符被改
	}
	for i, c := range bad {
		if c == "" {
			continue // 空游标表示新会话，由调用方约定，不在此用例
		}
		_, err := s.Scan(c, 2)
		if !errors.Is(err, ErrInvalidCursor) {
			t.Fatalf("case %d: want ErrInvalidCursor, got %v", i, err)
		}
	}
}

// TestCursorAcrossSessions 跨会话/已失效会话返回 ErrSessionInvalid，
// 且与游标格式非法是不同类别。
func TestCursorAcrossSessions(t *testing.T) {
	s := seedStore(6)
	p1, _ := s.Scan("", 2)
	p2, _ := s.Scan("", 2) // 另一个会话

	// 用 p1 的游标扫 p2 所在集合：游标属于仍存在的 p1 会话，合法；
	// 真正的跨会话错误通过显式失效体现。
	s.InvalidateSession(p1.SessionID())
	_, err := s.Scan(p1.Cursor, 3)
	if !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("invalidated session: want ErrSessionInvalid, got %v", err)
	}
	if errors.Is(err, ErrInvalidCursor) {
		t.Fatal("ErrSessionInvalid must not also satisfy ErrInvalidCursor")
	}

	// 另一个会话仍然有效。
	ok, err := s.Scan(p2.Cursor, 2)
	if err != nil || len(ok.Objects) != 2 {
		t.Fatalf("unaffected session: %+v err=%v", ok, err)
	}

	// 两类错误互不相等、可判定区分。
	if errors.Is(ErrSessionInvalid, ErrInvalidCursor) {
		t.Fatal("error categories must be distinct")
	}
}

// TestCursorOpaque 游标不得直接暴露主键明文。
func TestCursorOpaque(t *testing.T) {
	s := seedStore(2)
	p, _ := s.Scan("", 1)
	if strings.Contains(p.Cursor, "k000") || strings.Contains(p.Cursor, "k001") {
		t.Fatalf("cursor leaks plaintext key: %q", p.Cursor)
	}
	if p.Cursor == "" {
		t.Fatal("truncated page must carry a cursor")
	}
	// 不同次编码使用随机 nonce：同位置游标密文不同但都可用。
	q, _ := s.Scan("", 1)
	if q.Cursor == p.Cursor {
		// 极小概率；nonce 随机，重复基本不可能
		t.Fatal("cursors should differ due to randomized encryption")
	}
	r, err := s.Scan(p.Cursor, 1)
	if err != nil || !reflectKeys(keys(r), []string{"k001"}) {
		t.Fatalf("cursor reuse: %v err=%v", keys(r), err)
	}
}

// TestCursorFromOtherStore 不同 Store 的密钥不同：游标无效而非会话错误。
func TestCursorFromOtherStore(t *testing.T) {
	s1 := seedStore(3)
	s2 := seedStore(3)
	p, _ := s1.Scan("", 1)
	_, err := s2.Scan(p.Cursor, 1)
	if !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("foreign cursor must fail integrity check, got %v", err)
	}
}
