// Package notify 组合 alertstore 与 suppress，按分组节奏发出通知，
// 发送失败保留状态等待下次 Tick 重试。所有方法可并发调用，
// 结果等价于某个串行顺序；send 回调在持锁下按组键序同步执行，
// 回调内不得再调用本对象的任何方法。
package notify

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"ontology/alertstore"
	"ontology/suppress"
)

// 拒绝原因（按此优先级只报第一个）：参数非法 > 时钟回退 > 状态类。
var (
	ErrInvalid = errors.New("notify: invalid argument")
	ErrClock   = errors.New("notify: clock rewind")
)

const maxNow = int64(1_000_000_000_000)

// Notification 是一次分组通知，Firing 与 Resolved 均为升序指纹且互不相交。
type Notification struct {
	Group    string
	Firing   []string
	Resolved []string
	At       int64
}

// Result 是 Tick 的结果，Sent 与 Failed 均按组键字节序排列。
type Result struct {
	Sent   []string
	Failed []string
}

type groupState struct {
	hasSent  bool
	lastSent int64
	lastSet  map[string]bool
}

// Notifier 是告警分组通知器。
type Notifier struct {
	mu     sync.Mutex
	store  *alertstore.Store
	sil    *suppress.Silencer
	inh    *suppress.Inhibitor
	groups []string
	wait   int64
	repeat int64
	clock  int64
	states map[string]*groupState
}

// New 校验构造参数并创建 Notifier。
func New(groups []string, wait, repeat int64, maxActive int, rules []suppress.Rule) (*Notifier, error) {
	if wait < 0 || wait > 1e9 || repeat < 0 || repeat > 1e9 {
		return nil, fmt.Errorf("%w: wait/repeat out of [0,1e9]", ErrInvalid)
	}
	if maxActive < 1 || maxActive > 1e5 {
		return nil, fmt.Errorf("%w: maxActive out of [1,1e5]", ErrInvalid)
	}
	for _, g := range groups {
		if g == "" || len(g) > 64 {
			return nil, fmt.Errorf("%w: bad group label %q", ErrInvalid, g)
		}
	}
	for _, r := range rules {
		if err := suppress.CheckRule(r); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
	}
	gs := make([]string, len(groups))
	copy(gs, groups)
	return &Notifier{
		store: alertstore.New(maxActive), sil: suppress.NewSilencer(),
		inh: suppress.NewInhibitor(rules), groups: gs,
		wait: wait, repeat: repeat, states: make(map[string]*groupState),
	}, nil
}

func (n *Notifier) admit(now int64) error {
	if now < 0 || now > maxNow {
		return fmt.Errorf("%w: now %d out of [0,1e12]", ErrInvalid, now)
	}
	if now < n.clock {
		return fmt.Errorf("%w: now %d < accepted %d", ErrClock, now, n.clock)
	}
	return nil
}

// Fire 上报或去重告警；新指纹超上限返回 alertstore.ErrFull。
func (n *Notifier) Fire(now int64, l alertstore.Labels) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if err := alertstore.CheckLabels(l); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := n.admit(now); err != nil {
		return err
	}
	if err := n.store.Fire(now, l); err != nil {
		return err
	}
	n.clock = now
	return nil
}

// Resolve 解除告警；非 firing 返回 alertstore.ErrNotFiring。
func (n *Notifier) Resolve(now int64, l alertstore.Labels) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if err := alertstore.CheckLabels(l); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := n.admit(now); err != nil {
		return err
	}
	if err := n.store.Resolve(now, l); err != nil {
		return err
	}
	n.clock = now
	return nil
}

// AddSilence 新增静默；编号重复返回 suppress.ErrConflict。
func (n *Notifier) AddSilence(now int64, id string, m suppress.Matchers, start, end int64) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if id == "" {
		return fmt.Errorf("%w: empty silence id", ErrInvalid)
	}
	if err := suppress.CheckMatchers(m); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if start < 0 || start > maxNow || end < 0 || end > maxNow || start >= end {
		return fmt.Errorf("%w: bad silence window [%d,%d)", ErrInvalid, start, end)
	}
	if err := n.admit(now); err != nil {
		return err
	}
	if err := n.sil.Add(id, m, start, end); err != nil {
		return err
	}
	n.clock = now
	return nil
}

// ExpireSilence 把静默提前结束为 min(end, now)；不存在返回 suppress.ErrNotFound。
func (n *Notifier) ExpireSilence(now int64, id string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if id == "" {
		return fmt.Errorf("%w: empty silence id", ErrInvalid)
	}
	if err := n.admit(now); err != nil {
		return err
	}
	if err := n.sil.Expire(now, id); err != nil {
		return err
	}
	n.clock = now
	return nil
}

func (n *Notifier) groupKey(l alertstore.Labels) string {
	parts := make([]string, len(n.groups))
	for i, g := range n.groups {
		parts[i] = l[g]
	}
	return strings.Join(parts, "\x00")
}
