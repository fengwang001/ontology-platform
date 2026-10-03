package gate

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"ontology/meta"
)

type scriptedHooks struct {
	fail map[string]bool // 失败节点 -> 本次 Raise 内只失败一次
	log  []string
}

func newScripted(failNodes ...string) *scriptedHooks {
	m := make(map[string]bool)
	for _, n := range failNodes {
		m[n] = true
	}
	return &scriptedHooks{fail: m}
}

func (h *scriptedHooks) ack(node string, target int) error {
	h.log = append(h.log, fmt.Sprintf("Ack(%s,%d)", node, target))
	if h.fail[node] {
		h.fail[node] = false
		return fmt.Errorf("boom %s", node)
	}
	return nil
}

func (h *scriptedHooks) release(node string, target int) {
	h.log = append(h.log, fmt.Sprintf("Release(%s,%d)", node, target))
}

func sp(s string) *string     { return &s }
func st(v []string) *[]string { return &v }

func errBase(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Base.Error()
	}
	if err == nil {
		return ""
	}
	return err.Error()
}

func TestSpecEndToEnd(t *testing.T) {
	h := newScripted()
	s, err := New(h.ack, h.release)
	if err != nil {
		t.Fatal(err)
	}
	must := func(step string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", step, err)
		}
	}
	must("join a", s.Join("a", 3))
	must("raise2", s.Raise(2))
	must("raise3", s.Raise(3))
	must("put j", s.Put("a", "j", meta.Fields{Size: 5, Etag: sp("e"), Tags: st([]string{"t"})}))
	if st0 := s.Stats(); st0.G != 3 || st0.Count[3] != 1 {
		t.Fatalf("Stats = %+v", st0)
	}
	must("join b", s.Join("b", 2))

	v, err := s.Get("b", "j")
	must("get b", err)
	if v.Degraded != true || v.Etag == nil || *v.Etag != "e" || v.Tags != nil || v.Size != 5 {
		t.Fatalf("降级读 = %+v", v)
	}
	if err := s.Put("b", "k", meta.Fields{Size: 1, Etag: sp("x")}); errBase(err) != ErrReadOnly.Error() {
		t.Fatalf("降级节点写应报节点只读, got %v", err)
	}
	err = s.Rollback(2)
	if errBase(err) != ErrResidue.Error() || !strings.Contains(err.Error(), "残留 1 条") || !strings.Contains(err.Error(), "最高级别 3") {
		t.Fatalf("残留判定 = %v", err)
	}
	if st0 := s.Stats(); st0.G != 3 {
		t.Fatalf("被拒绝的 Rollback 不得改变 G, got %d", st0.G)
	}
	must("delete by b", s.Delete("b", "j"))
	must("rollback2", s.Rollback(2))
	if st0 := s.Stats(); st0.G != 2 {
		t.Fatalf("回退后 G=2, got %d", st0.G)
	}
	must("b 恢复可写", s.Put("b", "k", meta.Fields{Size: 1, Etag: sp("x")}))
	err = s.Raise(3)
	if errBase(err) != ErrNodeLagging.Error() || !strings.Contains(err.Error(), "b") {
		t.Fatalf("应报节点落后(b), got %v", err)
	}
}

func TestRaiseAckFailureSequences(t *testing.T) {
	cases := []struct {
		name string
		fail string
		want []string
	}{
		{"中间失败", "b", []string{"Ack(a,2)", "Ack(b,2)", "Release(a,2)"}},
		{"末个失败", "c", []string{"Ack(a,2)", "Ack(b,2)", "Ack(c,2)", "Release(b,2)", "Release(a,2)"}},
		{"首个失败", "a", []string{"Ack(a,2)"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newScripted(tc.fail)
			s, _ := New(h.ack, h.release)
			for _, n := range []string{"a", "b", "c"} {
				if err := s.Join(n, 3); err != nil {
					t.Fatal(err)
				}
			}
			err := s.Raise(2)
			if errBase(err) != ErrAckFailed.Error() || !strings.Contains(err.Error(), tc.fail) {
				t.Fatalf("应报确认失败(%s), got %v", tc.fail, err)
			}
			if !reflect.DeepEqual(h.log, tc.want) {
				t.Fatalf("调用序列 = %v, want %v", h.log, tc.want)
			}
			if st0 := s.Stats(); st0.G != 1 {
				t.Fatalf("Raise 失败后 G 不变, got %d", st0.G)
			}
			if err := s.Raise(2); err != nil {
				t.Fatalf("故障节点恢复后重试应成功, got %v", err)
			}
		})
	}
}

func TestRaiseTieAndStep(t *testing.T) {
	h := newScripted()
	s, _ := New(h.ack, h.release)
	if err := s.Raise(3); errBase(err) != ErrInvalidArg.Error() {
		t.Fatalf("G=1 Raise(3) 应参数非法, got %v", err)
	}
	if err := s.Raise(1); errBase(err) != ErrInvalidArg.Error() {
		t.Fatalf("Raise(1) 应参数非法, got %v", err)
	}
	must := func(step string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", step, err)
		}
	}
	must("join w", s.Join("w", 2))
	must("join x", s.Join("x", 2))
	must("join y", s.Join("y", 3))
	must("raise2", s.Raise(2))
	err := s.Raise(3)
	if errBase(err) != ErrNodeLagging.Error() || !strings.HasSuffix(err.Error(), "（节点 w）") {
		t.Fatalf("并列应报字节序最小者 w, got %v", err)
	}
	must("leave w", s.Leave("w"))
	must("leave x", s.Leave("x"))
	must("raise3", s.Raise(3))

	// 无节点
	h2 := newScripted()
	s2, _ := New(h2.ack, h2.release)
	if err := s2.Raise(2); errBase(err) != ErrNoNode.Error() {
		t.Fatalf("空集群应报无节点, got %v", err)
	}
}

func TestPutValidationOrderAndFields(t *testing.T) {
	h := newScripted()
	s, _ := New(h.ack, h.release)
	// 参数非法先于节点不在册
	if err := s.Put("", "k", meta.Fields{Size: 1}); errBase(err) != ErrInvalidArg.Error() {
		t.Fatalf("空节点名应参数非法, got %v", err)
	}
	if err := s.Put("a", "", meta.Fields{Size: 1}); errBase(err) != ErrInvalidArg.Error() {
		t.Fatalf("空 key 应参数非法, got %v", err)
	}
	if err := s.Put("a", "k", meta.Fields{Size: -1}); errBase(err) != ErrInvalidArg.Error() {
		t.Fatalf("size<0 应参数非法, got %v", err)
	}
	if err := s.Put("a", "k", meta.Fields{Size: 1e12 + 1}); errBase(err) != ErrInvalidArg.Error() {
		t.Fatalf("size>1e12 应参数非法, got %v", err)
	}
	if err := s.Put("a", "k", meta.Fields{Size: 1}); errBase(err) != ErrNotExist.Error() {
		t.Fatalf("应报节点不存在, got %v", err)
	}
	must := func(step string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", step, err)
		}
	}
	must("join a1", s.Join("a", 1))
	if err := s.Put("a", "k", meta.Fields{Size: 1, Etag: sp("e")}); errBase(err) != ErrUnsupported.Error() {
		t.Fatalf("G=1 提供 etag 应字段不支持, got %v", err)
	}
	if err := s.Put("a", "k", meta.Fields{Size: 1, Tags: st(nil)}); errBase(err) != ErrUnsupported.Error() {
		t.Fatalf("G=1 提供空 tags 也应字段不支持, got %v", err)
	}
	// size 边界与缺省/空串区别
	must("size=0", s.Put("a", "z", meta.Fields{Size: 0}))
	must("size=1e12", s.Put("a", "q", meta.Fields{Size: 1e12}))

	// Get/Delete 参数非法先于节点不在册先于键不存在
	if _, err := s.Get("", "k"); errBase(err) != ErrInvalidArg.Error() {
		t.Fatalf("Get 空节点应参数非法, got %v", err)
	}
	if _, err := s.Get("nope", "k"); errBase(err) != ErrNotExist.Error() {
		t.Fatalf("Get 未知节点应不存在, got %v", err)
	}
	if _, err := s.Get("a", "nope"); errBase(err) != ErrNotExist.Error() {
		t.Fatalf("Get 未知键应不存在, got %v", err)
	}
	if err := s.Delete("nope", "z"); errBase(err) != ErrNotExist.Error() {
		t.Fatalf("Delete 未知节点应不存在, got %v", err)
	}
}

func TestAbsentVsEmptyAndRecoverAfterRollback(t *testing.T) {
	h := newScripted()
	s, _ := New(h.ack, h.release)
	must := func(step string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", step, err)
		}
	}
	must("join a3", s.Join("a", 3))
	must("raise2", s.Raise(2))
	must("raise3", s.Raise(3))
	must("join b1", s.Join("b", 1))
	// 缺省 etag，但空串 tags；两者语义不同
	must("put", s.Put("a", "k", meta.Fields{Size: 7, Tags: st([]string{})}))
	v, err := s.Get("a", "k")
	must("get", err)
	if v.Etag != nil {
		t.Fatal("缺省 etag 应保持 nil")
	}
	if v.Tags == nil || !reflect.DeepEqual(*v.Tags, []string{}) {
		t.Fatalf("空集合 tags 应已提供: %+v", v)
	}
	// b 在 G=3 下降级：能 Delete 但不能写
	if err := s.Put("b", "k2", meta.Fields{Size: 1}); errBase(err) != ErrReadOnly.Error() {
		t.Fatalf("b 应只读, got %v", err)
	}
	must("b 删除高级别记录", s.Delete("b", "k"))
	must("rollback2", s.Rollback(2))
	must("rollback1", s.Rollback(1))
	must("b 恢复可写", s.Put("b", "k2", meta.Fields{Size: 2}))
	if r, _ := s.Get("b", "k2"); r.Level != 1 || r.Degraded {
		t.Fatalf("回退后写入应为级别 1: %+v", r)
	}
}

func TestRollbackValidation(t *testing.T) {
	h := newScripted()
	s, _ := New(h.ack, h.release)
	if err := s.Rollback(0); errBase(err) != ErrInvalidArg.Error() {
		t.Fatalf("target=0 应参数非法, got %v", err)
	}
	if err := s.Rollback(1); errBase(err) != ErrInvalidArg.Error() {
		t.Fatalf("target==G 应参数非法, got %v", err)
	}
	if err := s.Rollback(2); errBase(err) != ErrInvalidArg.Error() {
		t.Fatalf("target>G 应参数非法, got %v", err)
	}
}
