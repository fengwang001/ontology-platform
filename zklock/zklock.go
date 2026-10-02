package zklock

import "sync"

// child 是一把锁上一个现存子节点的内部状态。
type child struct {
	lock       *lockState
	node       Node
	held       bool
	watchSeq   int // 当前观察登记 ws；无观察时为 -1
	watchEntry *watcher
}

// watcher 是观察登记条目，同时是按 ws 排序的双向循环链表节点。
type watcher struct {
	id     int
	target int
	ch     *child
	prev   *watcher
	next   *watcher
}

// lockState 是一把锁的全部状态。
type lockState struct {
	name    string
	cs      int // 序号计数，永不复用
	size    int
	all     *avlNode // 全部现存子节点，按序号
	writers *avlNode // 现存写者子节点，按序号
	watches map[int]*watcher
}

func newLock(name string) *lockState {
	return &lockState{
		name:    name,
		watches: make(map[int]*watcher),
	}
}

// watchSentinel 返回（惰性创建）观察目标 target 的循环链表哨兵。
func (lk *lockState) watchSentinel(target int) *watcher {
	s := lk.watches[target]
	if s == nil {
		s = &watcher{id: -1}
		s.prev = s
		s.next = s
		lk.watches[target] = s
	}
	return s
}

// addWatch 以登记序号 id 把 ch 登记为观察 target 的等待者（排在队尾）。
func (lk *lockState) addWatch(ch *child, target, id int) {
	s := lk.watchSentinel(target)
	w := &watcher{id: id, target: target, ch: ch, prev: s.prev, next: s}
	s.prev.next = w
	s.prev = w
	ch.watchSeq = id
	ch.watchEntry = w
}

// removeWatch 撤销 ch 当前的观察登记。
func (lk *lockState) removeWatch(ch *child) {
	w := ch.watchEntry
	if w == nil {
		return
	}
	w.prev.next = w.next
	w.next.prev = w.prev
	s := lk.watches[w.target]
	if s != nil && s.next == s {
		delete(lk.watches, w.target)
	}
	ch.watchEntry = nil
	ch.watchSeq = -1
}

// coordinator 内部实现见 Coordinator。
type coordinatorImpl struct {
	mu       sync.Mutex
	C        int
	nextSid  int
	zxid     int
	ws       int
	sessions map[int]bool
	locks    map[string]*lockState
	owns     map[int]map[*child]bool

	reevaluations int
	evalTouches   int
}

// isValidKind 判断 kind 是否合法。
func isValidKind(kind Kind) bool {
	return kind == Read || kind == Write
}

func (c *Coordinator) impl() *coordinatorImpl { return c.inner }

// evaluate 按配方规则评估一个等待者：授予或登记观察。
// 调用时被删除节点已不在树中。返回 true 表示本次被授予。
// 每访问（比较）一个树节点就计入 evalTouches。
func (ci *coordinatorImpl) evaluate(lk *lockState, ch *child, newWS func() int) bool {
	n := ch.node.N
	if ch.node.Kind == Write {
		// 写者：自己是现存最小者则持有；否则观察序号小于自己的最大者（任意 kind）。
		// maxBelow(all, n) 为空当且仅当 n 是现存最小者，一次下行遍历即可判定。
		var pred *avlNode
		t := lk.all
		for t != nil {
			ci.evalTouches++
			if t.key < n {
				pred = t
				t = t.right
			} else {
				t = t.left
			}
		}
		if pred == nil {
			ch.held = true
			return true
		}
		id := newWS()
		lk.addWatch(ch, pred.key, id)
		return false
	}
	// 读者：不存在序号小于自己的 W 则持有；否则观察其中序号最大者。
	var predW *avlNode
	t := lk.writers
	for t != nil {
		ci.evalTouches++
		if t.key < n {
			predW = t
			t = t.right
		} else {
			t = t.left
		}
	}
	if predW == nil {
		ch.held = true
		return true
	}
	id := newWS()
	lk.addWatch(ch, predW.key, id)
	return false
}

// notifyDeletion 在 target 节点删除后，按 ws 升序逐个重新评估其观察者。
func (ci *coordinatorImpl) notifyDeletion(lk *lockState, target, delZxid int, events []Grant) []Grant {
	s := lk.watches[target]
	if s == nil {
		return events
	}
	for w := s.next; w != s; {
		next := w.next
		ch := w.ch
		// 先从当前（已失效的）观察登记中摘除。
		w.prev.next = w.next
		w.next.prev = w.prev
		ch.watchEntry = nil
		ch.watchSeq = -1

		ci.reevaluations++
		granted := ci.evaluate(lk, ch, func() int {
			ci.ws++
			return ci.ws
		})
		if granted {
			events = append(events, Grant{
				Lock:        lk.name,
				N:           ch.node.N,
				Kind:        ch.node.Kind,
				Session:     ch.node.Session,
				TriggerZxid: delZxid,
			})
		}
		w = next
	}
	delete(lk.watches, target)
	return events
}

// deleteChild 在树与索引中删除节点（不处理观察与通知）。
func (ci *coordinatorImpl) deleteChild(lk *lockState, ch *child) {
	lk.all = avlDelete(lk.all, ch.node.N)
	lk.size--
	if ch.node.Kind == Write {
		lk.writers = avlDelete(lk.writers, ch.node.N)
	}
	if set := ci.owns[ch.node.Session]; set != nil {
		delete(set, ch)
		if len(set) == 0 {
			delete(ci.owns, ch.node.Session)
		}
	}
}
