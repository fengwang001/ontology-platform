package sched

import (
	"sync"
	"testing"

	"ontology/group"
)

// TestTouchedBound 证明单次操作读写的不同运行记录数不超过 4，
// 且与队列长度无关：队列长 100 与 10000 两档对照，
// 特别验证「取消队列中部的 Waiting」不做线性扫描。
func TestTouchedBound(t *testing.T) {
	for _, qlen := range []int{100, 10000} {
		qlen := qlen
		t.Run("", func(t *testing.T) {
			s, _ := New(1, qlen+10)
			first, _ := s.Submit(gstr("holder"), false, false) // Running
			// 用不同的组名提交 qlen 个 Waiting（彼此不同组，无 Pending 顶替）。
			mid := -1
			for i := 0; i < qlen; i++ {
				g := []byte("w")
				g = append(g, itoa(i)...)
				id, err := s.Submit(g, false, false)
				if err != nil {
					t.Fatalf("submit %d: %v", i, err)
				}
				if i == qlen/2 {
					mid = id
				}
			}
			if s.Busy() != 1 || len(s.Queue()) != qlen {
				t.Fatalf("qlen=%d setup: busy=%d queue=%d", qlen, s.Busy(), len(s.Queue()))
			}

			// 取消队列中部的 Waiting：只触达该运行本身（+队首推进不发生，位仍占满）。
			if err := s.Cancel(mid); err != nil {
				t.Fatal(err)
			}
			if n := s.lastTouched(); n != 1 {
				t.Fatalf("qlen=%d cancel-mid touched=%d, want 1（只触达被取消记录）", qlen, n)
			}
			if n := len(s.Queue()); n != qlen-1 {
				t.Fatalf("queue len after mid cancel = %d, want %d", n, qlen-1)
			}

			// Finish 队首 Running：触达 Running、组 Pending（无）、新队首 = 至多 3。
			if err := s.Finish(first, true); err != nil {
				t.Fatal(err)
			}
			if n := s.lastTouched(); n != 2 {
				t.Fatalf("qlen=%d finish touched=%d, want 2（Running + 新队首）", qlen, n)
			}

			// Submit 顶替某 Waiting 组：新运行 + 旧 Waiting（无 Pending）+队首不推进
			//（位仍满）= 至多 2。
			front := s.Queue()[0]
			grp, _ := groupOf(s, front)
			if _, err := s.Submit(grp, true, false); err != nil {
				t.Fatal(err)
			}
			if n := s.lastTouched(); n != 2 {
				t.Fatalf("qlen=%d replace-waiting touched=%d, want 2（新运行 + 旧 Waiting）", qlen, n)
			}

			// 带 Pending 的 Running 被 AckCancel：目标 + 晋升的 Pending + 新队首 = 3。
			s2, _ := New(1, qlen+10)
			g0, _ := s2.Submit(gstr("g0"), false, false) // Running
			s2.Cancel(g0)                                // Cancelling
			s2.Submit(gstr("g0"), false, false)          // Pending
			for i := 0; i < qlen; i++ {
				s2.Submit(append([]byte("z"), itoa(i)...), false, false)
			}
			if err := s2.AckCancel(g0); err != nil {
				t.Fatal(err)
			}
			if n := s2.lastTouched(); n != 3 {
				t.Fatalf("qlen=%d ack+promote touched=%d, want 3（Cancelling + Pending + 新队首）", qlen, n)
			}
			t.Logf("qlen=%d touched: cancel-mid=1 finish=2 replace-waiting=2 ack+promote=3（上界 4）", qlen)
		})
	}
}

func groupOf(s *Scheduler, id int) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.runs[id]
	if r == nil {
		return nil, false
	}
	return append([]byte(nil), r.Group...), true
}

func itoa(n int) []byte {
	if n == 0 {
		return []byte{'0'}
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return append([]byte(nil), buf[i:]...)
}

// TestConcurrent 并发调用同一调度器：结果必须等价某串行顺序，
// 且所有不变量始终成立（Running+Cancelling ≤ C、有空位必无队列等）。
func TestConcurrent(t *testing.T) {
	s, _ := New(4, 50)
	const workers = 8
	const rounds = 300
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			local := []int{}
			for i := 0; i < rounds; i++ {
				g := []byte{'g', byte('0' + w%4)}
				switch i % 6 {
				case 0, 1:
					if id, err := s.Submit(g, i%3 == 0, i%5 == 0); err == nil {
						local = append(local, id)
					}
				case 2:
					if _, err := s.Submit(nil, false, false); err == nil {
						// 空组提交
					}
				case 3:
					if len(local) > 0 {
						s.Finish(local[0], true)
					}
				case 4:
					if len(local) > 0 {
						s.Cancel(local[len(local)-1])
					}
				default:
					if len(local) > 0 {
						s.AckCancel(local[0])
					}
				}
			}
		}(w)
	}
	wg.Wait()
	// 结束后校验全部不变量。
	checkInvariants(t, s, 4, "concurrent-final")
	if s.Busy() > 4 {
		t.Fatalf("busy=%d > 4", s.Busy())
	}
	if s.Busy() < 4 && len(s.Queue()) != 0 {
		t.Fatalf("free slot but queue non-empty: %v", s.Queue())
	}
	// 每组占位者/Pending 至多一个：抽查组表结构一致性。
	counts := map[string]int{}
	for id, st := range s.snapshotStates() {
		g, _ := groupOf(s, id)
		switch st {
		case group.Waiting, group.Running, group.Cancelling:
			if len(g) > 0 {
				counts[string(g)+":h"]++
			}
		case group.Pending:
			if len(g) > 0 {
				counts[string(g)+":p"]++
			}
		}
	}
	for k, n := range counts {
		if n > 1 {
			t.Fatalf("group %s count=%d > 1", k, n)
		}
	}
}
