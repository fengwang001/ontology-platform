package batchisolate

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// errPermanent 是与 ErrTransient 无关的永久错误。
var errPermanent = errors.New("permanent failure")

type call struct {
	ids []string
}

// scriptSink 按“同一有序批”的调用次数脚本化返回错误。
//
//	poison：毒丸集合；批与毒丸相交即永久失败（整批原子语义），优先级最高。
//	transient：以批的有序内容为 key，前 n 次调用该批返回瞬时错误，之后成功。
type scriptSink struct {
	mu        sync.Mutex
	poison    map[string]bool
	transient map[string]int
	tries     map[string]int
	log       []call
}

func setKey(ids []string) string {
	return fmt.Sprintf("%q", ids)
}

func newScriptSink(poison []string, transient map[string]int) *scriptSink {
	s := &scriptSink{
		poison:    map[string]bool{},
		transient: map[string]int{},
		tries:     map[string]int{},
	}
	for _, p := range poison {
		s.poison[p] = true
	}
	for k, v := range transient {
		s.transient[k] = v
	}
	return s
}

func (s *scriptSink) Write(ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log = append(s.log, call{ids: append([]string(nil), ids...)})
	for _, id := range ids {
		if s.poison[id] {
			return errPermanent
		}
	}
	key := setKey(ids)
	s.tries[key]++
	if s.tries[key] <= s.transient[key] {
		return ErrTransient
	}
	return nil
}

func (s *scriptSink) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.log)
}

func deadIDs(dead []DeadLetter) []string {
	out := make([]string, len(dead))
	for i, d := range dead {
		out[i] = d.ID
	}
	return out
}

func deadReasons(dead []DeadLetter) map[string]Reason {
	out := map[string]Reason{}
	for _, d := range dead {
		out[d.ID] = d.Reason
	}
	return out
}

func mustNew(t *testing.T, r, km, cmax int, sink SinkFunc) *Isolator {
	t.Helper()
	iso, err := New(r, km, cmax, sink)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return iso
}

func idsRange(prefix string, n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("%s%d", prefix, i)
	}
	return ids
}
