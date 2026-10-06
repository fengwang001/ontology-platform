package channel

import (
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentLinearizable 在 -race 下高并发混杂执行全部操作：
//   - 多发送者竞争序号：成功条数必须恰好等于最终 Latest，且无洞、无重号；
//   - 读者持续查询 Unread/UnreadMentions/Fetch，只需不 panic 且结果自洽；
//   - 读者反复 MarkRead 到最新，验证读水位单调不减。
func TestConcurrentLinearizable(t *testing.T) {
	c := newTestChannel(t)
	must(t, c.Join("alice", 0))
	must(t, c.Join("bob", 0))
	must(t, c.Join("carol", 0))
	must(t, c.Promote("alice", "bob", 0))

	const senders = 8
	const perSender = 200
	var sendWg, readerWg sync.WaitGroup

	// 时钟由原子计数器单调发号。
	var clock atomic.Int64
	nextNow := func() int64 {
		return clock.Add(1)
	}

	seqs := make(chan int64, senders*perSender)
	for g := 0; g < senders; g++ {
		sendWg.Add(1)
		go func(id int) {
			defer sendWg.Done()
			user := []string{"alice", "bob", "carol"}[id%3]
			for i := 0; i < perSender; i++ {
				now := nextNow()
				seq, err := c.Send(user, "concurrent", []string{"bob"}, now)
				if err == nil {
					seqs <- seq
				}
			}
		}(g)
	}

	// 读者：持续 MarkRead（推进到最新）+ Unread/UnreadMentions + Fetch。
	stop := make(chan struct{})
	readerWg.Add(1)
	go func() {
		defer readerWg.Done()
		var lastWM int64
		for {
			select {
			case <-stop:
				return
			default:
			}
			now := nextNow()
			latest := c.Latest()
			if err := c.MarkRead("carol", latest, now); err == nil {
				wm, _ := c.Watermark("carol")
				if wm < lastWM {
					t.Errorf("watermark moved backward: %d -> %d", lastWM, wm)
				}
				lastWM = wm
			}
			u, err := c.Unread("bob")
			if err == nil && u < 0 {
				t.Errorf("negative unread: %d", u)
			}
			um, err := c.UnreadMentions("bob")
			if err == nil && um < 0 {
				t.Errorf("negative mention unread: %d", um)
			}
			// 注：Unread 与 UnreadMentions 是两次独立加锁查询，在并发下可能
			// 读到不同串行点的快照，因此此处不做跨快照的大小比较；
			// 单快照不变量（0 <= unreadMentions <= 同快照 unread）由
			// 朴素模型差分测试在等价串行序列上严格保证。
			_ = u
			if _, err := c.Fetch("bob", 0, 10, now); err != nil && err != ErrClockRewind {
				t.Errorf("fetch: %v", err)
			}
		}
	}()

	// 管理员随机撤回（只撤回别人早期消息），验证并发撤回/计数安全。
	readerWg.Add(1)
	go func() {
		defer readerWg.Done()
		for i := 0; i < 100; i++ {
			now := nextNow()
			latest := c.Latest()
			if latest > 10 {
				select {
				case <-stop:
					return
				default:
				}
				_ = c.Recall("bob", (latest%50)+1, now)
			}
		}
	}()

	sendWg.Wait()
	close(stop)
	readerWg.Wait()

	close(seqs)
	got := map[int64]bool{}
	count := 0
	for s := range seqs {
		if got[s] {
			t.Fatalf("duplicate seq %d under concurrency", s)
		}
		got[s] = true
		count++
	}
	latest := c.Latest()
	if int64(count) != latest {
		t.Fatalf("successful sends = %d but latest = %d", count, latest)
	}
	for s := int64(1); s <= latest; s++ {
		if !got[s] {
			t.Fatalf("sequence hole at %d (latest %d)", s, latest)
		}
	}
}

// TestReplayDeterministic 用固定脚本在两个独立频道上重放，
// 逐步比较全部可观察结果，证明相同输入序列产生完全相同的结果。
func TestReplayDeterministic(t *testing.T) {
	script := []func(c *Channel) (string, error){
		func(c *Channel) (string, error) { return "", c.Join("alice", 0) },
		func(c *Channel) (string, error) { return "", c.Join("bob", 1) },
		func(c *Channel) (string, error) {
			s, e := c.Send("alice", "m", []string{"bob"}, 2)
			return seqStr(s), e
		},
		func(c *Channel) (string, error) { s, e := c.Send("bob", "self", nil, 3); return seqStr(s), e },
		func(c *Channel) (string, error) { return "", c.MarkRead("bob", 2, 4) },
		func(c *Channel) (string, error) { u, e := c.Unread("bob"); return nStr(u), e },
		func(c *Channel) (string, error) { u, e := c.UnreadMentions("bob"); return nStr(u), e },
		func(c *Channel) (string, error) { return "", c.Edit("alice", 1, "m2", nil, 5) },
		func(c *Channel) (string, error) { u, e := c.UnreadMentions("bob"); return nStr(u), e },
		func(c *Channel) (string, error) { return "", c.Recall("alice", 2, 6) },
		func(c *Channel) (string, error) { u, e := c.Unread("bob"); return nStr(u), e },
	}
	c1 := newTestChannel(t)
	c2 := newTestChannel(t)
	for i, step := range script {
		v1, e1 := step(c1)
		v2, e2 := step(c2)
		if v1 != v2 || errStr(e1) != errStr(e2) {
			t.Fatalf("step %d replay differs: (%s,%v) vs (%s,%v)", i, v1, e1, v2, e2)
		}
	}
}

func seqStr(s int64) string { return "seq=" + nStr(s) }
func nStr(v int64) string   { return strconv.FormatInt(v, 10) }
func errStr(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}
