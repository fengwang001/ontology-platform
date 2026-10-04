package guard

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"
)

type simParams struct {
	tz, cs, ce, lw, lh, hb int64
	dmax                   int
}

// naive 是逐秒推进的参考实现，判定规则与事件驱动版本保持一致。
type naive struct {
	p       simParams
	holiday map[int64]bool
	reg     map[string]bool
	last    map[string]int64 // key acct|dev -> last，仅在线设备
	used    map[string]int64 // key acct|day -> seconds
	now     int64
	maxNow  int64
}

func newNaive(p simParams) *naive {
	return &naive{
		p:       p,
		holiday: map[int64]bool{},
		reg:     map[string]bool{},
		last:    map[string]int64{},
		used:    map[string]int64{},
	}
}

func simFloor(a, b int64) int64 {
	q := a / b
	if a%b < 0 {
		q--
	}
	return q
}

func simKey(a string, d int64) string { return fmt.Sprintf("%s|%d", a, d) }

func devKey(a, dev string) string { return a + "|" + dev }

func (n *naive) day(t int64) int64 { return simFloor(t+n.p.tz, 86400) }

func (n *naive) xod(t int64) int64 {
	u := t + n.p.tz
	return u - simFloor(u, 86400)*86400
}

func (n *naive) curfew(t int64) bool {
	if n.p.cs == n.p.ce {
		return false
	}
	x := n.xod(t)
	if n.p.cs < n.p.ce {
		return x >= n.p.cs && x < n.p.ce
	}
	return x >= n.p.cs || x < n.p.ce
}

func (n *naive) limit(d int64) int64 {
	if n.holiday[d] {
		return n.p.lh
	}
	return n.p.lw
}

func (n *naive) usedOf(a string, d int64) int64 { return n.used[simKey(a, d)] }

func (n *naive) onlineDevs(a string) []string {
	prefix := a + "|"
	var out []string
	for k := range n.last {
		if strings.HasPrefix(k, prefix) {
			out = append(out, strings.TrimPrefix(k, prefix))
		}
	}
	sort.Strings(out)
	return out
}

func (n *naive) anyOnline(a string) bool { return len(n.onlineDevs(a)) > 0 }

// tickTo 逐秒从 now+1 推进到 target。
func (n *naive) tickTo(target int64) {
	for t := n.now + 1; t <= target; t++ {
		oldDay := n.day(t - 1)
		// 1) 结算区间 (t-1, t)：t-1 时在线、非宵禁、旧日额度未满，计 1 秒。
		accs := map[string]bool{}
		for k := range n.last {
			accs[strings.SplitN(k, "|", 2)[0]] = true
		}
		for a := range accs {
			if n.anyOnline(a) && !n.curfew(t-1) && n.usedOf(a, oldDay) < n.limit(oldDay) {
				n.used[simKey(a, oldDay)]++
				// 恰在某秒用满旧日额度：该秒末立即强制下线（含恰好日末）。
				if n.usedOf(a, oldDay) >= n.limit(oldDay) {
					for _, d := range n.onlineDevs(a) {
						delete(n.last, devKey(a, d))
					}
				}
			}
		}
		// 2) t 时刻事件：心跳超时取等离线。
		for k, last := range n.last {
			if last+n.p.hb <= t {
				delete(n.last, k)
			}
		}
		// 3) 宵禁进入 或 日界跨入 0 额度日：强制下线。
		newDay := n.day(t)
		for a := range accs {
			if !n.anyOnline(a) {
				continue
			}
			if n.curfew(t) || (newDay != oldDay && n.limit(newDay) == 0) {
				for _, d := range n.onlineDevs(a) {
					delete(n.last, devKey(a, d))
				}
			}
		}
		n.now = t
	}
	if target > n.maxNow {
		n.maxNow = target
	}
}

type simOpKind int

const (
	simLogin simOpKind = iota
	simHeartbeat
	simLogout
)

type simOp struct {
	kind simOpKind
	now  int64
	acct string
	dev  string
}

func (n *naive) run(o simOp) error {
	if o.now < n.maxNow {
		return ErrClockBack
	}
	if !n.reg[o.acct] {
		n.tickTo(o.now)
		return ErrNoAccount
	}
	n.tickTo(o.now)
	key := devKey(o.acct, o.dev)
	switch o.kind {
	case simLogin:
		if n.curfew(o.now) {
			return ErrCurfew
		}
		d := n.day(o.now)
		if n.usedOf(o.acct, d) >= n.limit(d) {
			return ErrQuotaExhausted
		}
		if _, ok := n.last[key]; ok {
			return ErrDeviceOnline
		}
		if len(n.onlineDevs(o.acct)) >= n.p.dmax {
			return ErrDeviceLimit
		}
		n.last[key] = o.now
	case simHeartbeat:
		if _, ok := n.last[key]; !ok {
			return ErrDeviceOffline
		}
		n.last[key] = o.now
	case simLogout:
		if _, ok := n.last[key]; !ok {
			return ErrDeviceOffline
		}
		delete(n.last, key)
	}
	return nil
}

// TestConcurrentNoRace 并发调用不得死锁或产生数据竞争（-race 验证）；
// 每个 goroutine 使用独立单调时间轴，结果必然等价于某个合法串行顺序。
func TestConcurrentNoRace(t *testing.T) {
	g, err := New(0, 79200, 28800, 86400, 86400, 900, 4)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		g.Register(fmt.Sprintf("a%d", i))
	}
	var wg sync.WaitGroup
	for w := 0; w < 10; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(id) + 99))
			acct := fmt.Sprintf("a%d", id%6)
			dev := fmt.Sprintf("dev%d", id%4)
			now := int64(1000 + id) // 各 goroutine 独立时间轴起点
			for k := 0; k < 200; k++ {
				now += int64(1 + rng.Intn(200))
				switch rng.Intn(4) {
				case 0:
					_ = g.Login(now, acct, dev)
				case 1:
					_ = g.Heartbeat(now, acct, dev)
				case 2:
					_ = g.Logout(now, acct, dev)
				case 3:
					_, _ = g.Remaining(now, acct)
				}
			}
		}(w)
	}
	wg.Wait()
}
