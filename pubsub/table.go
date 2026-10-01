package pubsub

import (
	"io"
	"log"
	"sort"
	"strings"
	"sync"
)

// Delivery 是一次发布投递给某个客户端的结果。
type Delivery struct {
	ClientID   string
	GrantedQoS int
}

// RetainedMessage 是订阅时随匹配过滤器下发的保留消息。
type RetainedMessage struct {
	Topic   string
	Payload string
}

// subNode 是订阅 Trie 的一个层级节点。
type subNode struct {
	literals map[string]*subNode
	// empty 是层级为 "" 的字面量子节点（空串无法靠 map 区分存在与否）。
	empty *subNode
	// plus 与 hash 单独挂出，便于按层级遍历。
	plus *subNode
	hash *subNode
	// subs 为恰好终止于本节点的订阅：客户端 -> 等级。
	subs map[string]int
}

func newSubNode() *subNode {
	return &subNode{literals: map[string]*subNode{}, subs: map[string]int{}}
}

func (n *subNode) literalChild(level string) *subNode {
	if level == "" {
		return n.empty
	}
	return n.literals[level]
}

func (n *subNode) setLiteralChild(level string, child *subNode) {
	if level == "" {
		n.empty = child
		return
	}
	n.literals[level] = child
}

func (n *subNode) deleteLiteralChild(level string) {
	if level == "" {
		n.empty = nil
		return
	}
	delete(n.literals, level)
}

func (n *subNode) isEmpty() bool {
	return len(n.subs) == 0 && len(n.literals) == 0 &&
		n.empty == nil && n.plus == nil && n.hash == nil
}

// Table 是并发安全的层级通配订阅表。
// 所有操作在同一把锁下完成，效果等价于某个全局串行顺序；
// 输出均按字节序排序，相同调用序列重放得到完全相同的结果。
type Table struct {
	mu       sync.RWMutex
	root     *subNode
	retained map[string]string
	logw     io.Writer
}

// NewTable 创建空订阅表，日志写入 logw（nil 表示不输出日志）。
func NewTable(logw io.Writer) *Table {
	return &Table{
		root:     newSubNode(),
		retained: map[string]string{},
		logw:     logw,
	}
}

func (t *Table) logf(format string, args ...any) {
	if t.logw == nil {
		return
	}
	log.New(t.logw, "pubsub ", log.LstdFlags|log.Lmicroseconds).Printf(format, args...)
}

// Subscribe 添加或覆盖（客户端, 过滤器）订阅并返回当前匹配的保留消息。
// 过滤器非法优先于等级越界；被拒绝时不改变任何状态。
func (t *Table) Subscribe(clientID, filter string, qos int) ([]RetainedMessage, error) {
	levels, err := validateFilter(filter)
	if err != nil {
		t.logf("SUBSCRIBE reject client=%q filter=%q qos=%d reason=%v", clientID, filter, qos, err)
		return nil, err
	}
	if qos < 0 || qos > 2 {
		t.logf("SUBSCRIBE reject client=%q filter=%q qos=%d reason=%v", clientID, filter, qos, ErrInvalidQoS)
		return nil, ErrInvalidQoS
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	node := t.root
	for _, level := range levels {
		node = t.walkOrCreate(node, level)
	}
	node.subs[clientID] = qos

	retained := t.matchingRetainedLocked(levels)
	t.logf("SUBSCRIBE ok client=%q filter=%q qos=%d retained=%d topics=%v",
		clientID, filter, qos, len(retained), retainedTopics(retained))
	return retained, nil
}

func (t *Table) walkOrCreate(node *subNode, level string) *subNode {
	switch level {
	case "+":
		if node.plus == nil {
			node.plus = newSubNode()
		}
		return node.plus
	case "#":
		if node.hash == nil {
			node.hash = newSubNode()
		}
		return node.hash
	}
	child := node.literalChild(level)
	if child == nil {
		child = newSubNode()
		node.setLiteralChild(level, child)
	}
	return child
}

// matchingRetainedLocked 朴素扫描全部保留消息，返回被过滤器命中者并按主题字节序排列。
func (t *Table) matchingRetainedLocked(filterLevels []string) []RetainedMessage {
	out := make([]RetainedMessage, 0)
	for topic, payload := range t.retained {
		if naiveMatch(filterLevels, splitLevels(topic)) {
			out = append(out, RetainedMessage{Topic: topic, Payload: payload})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Topic < out[j].Topic })
	return out
}

func retainedTopics(msgs []RetainedMessage) []string {
	topics := make([]string, len(msgs))
	for i, msg := range msgs {
		topics[i] = msg.Topic
	}
	return topics
}

// Unsubscribe 删除（客户端, 过滤器）订阅；过滤器非法或订阅不存在均拒绝且不改变状态。
func (t *Table) Unsubscribe(clientID, filter string) error {
	levels, err := validateFilter(filter)
	if err != nil {
		t.logf("UNSUBSCRIBE reject client=%q filter=%q reason=%v", clientID, filter, err)
		return err
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	path := make([]*subNode, 1, len(levels)+1)
	path[0] = t.root
	node := t.root
	for _, level := range levels {
		switch level {
		case "+":
			node = node.plus
		case "#":
			node = node.hash
		default:
			node = node.literalChild(level)
		}
		if node == nil {
			t.logf("UNSUBSCRIBE reject client=%q filter=%q reason=%v", clientID, filter, ErrNoSubscription)
			return ErrNoSubscription
		}
		path = append(path, node)
	}
	if _, ok := node.subs[clientID]; !ok {
		t.logf("UNSUBSCRIBE reject client=%q filter=%q reason=%v", clientID, filter, ErrNoSubscription)
		return ErrNoSubscription
	}
	delete(node.subs, clientID)

	// 从末层向上剪枝空节点。
	for i := len(levels); i >= 1; i-- {
		if !path[i].isEmpty() {
			break
		}
		parent, level := path[i-1], levels[i-1]
		switch level {
		case "+":
			parent.plus = nil
		case "#":
			parent.hash = nil
		default:
			parent.deleteLiteralChild(level)
		}
	}

	t.logf("UNSUBSCRIBE ok client=%q filter=%q", clientID, filter)
	return nil
}

// Publish 按主题匹配订阅者，返回去重、合并最大等级、按客户端标识升序的投递结果。
// retain 为真时该主题只保留最后一条；payload 为空串则清除该主题的保留消息。
func (t *Table) Publish(topic, payload string, retain bool) ([]Delivery, error) {
	levels, err := validateTopic(topic)
	if err != nil {
		t.logf("PUBLISH reject topic=%q retain=%v reason=%v", topic, retain, err)
		return nil, err
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	granted := map[string]int{}
	basis := map[string][]string{}
	addSubs := func(node *subNode, matchedFilter string) {
		for clientID, qos := range node.subs {
			if old, ok := granted[clientID]; !ok || qos > old {
				granted[clientID] = qos
			}
			basis[clientID] = append(basis[clientID], matchedFilter+"@qos"+itoa(qos))
		}
	}

	// 首层 '$' 主题不被首层 '#'（即根上的 '#' 边）匹配。
	rootBarsWild := len(levels[0]) > 0 && levels[0][0] == '$'
	if t.root.hash != nil && !rootBarsWild {
		addSubs(t.root.hash, "#")
	}

	type frame struct {
		node   *subNode
		prefix string // 到达该节点对应的过滤器前缀
		atRoot bool   // 该节点的子边是否属于过滤器首层
	}
	active := []frame{{node: t.root, prefix: "", atRoot: true}}

	for depth, level := range levels {
		next := make([]frame, 0, len(active)*2)
		for _, fr := range active {
			// 每深入一层，该路径上的 '#' 匹配父层之下任意多层。
			// '$' 例外只作用于过滤器首层：根节点上的 '#' 不匹配 '$' 主题。
			if fr.node.hash != nil && !(fr.atRoot && rootBarsWild) {
				addSubs(fr.node.hash, fr.prefix+"/#")
			}
			if child := fr.node.literalChild(level); child != nil {
				next = append(next, frame{node: child, prefix: joinFilter(fr.prefix, level)})
			}
			if fr.node.plus != nil {
				child := fr.node.plus
				// 首层 '+' 不匹配 '$' 主题。
				if fr.atRoot && rootBarsWild {
					child = nil
				}
				if child != nil {
					next = append(next, frame{node: child, prefix: joinFilter(fr.prefix, "+")})
				}
			}
		}
		active = next
		for i := range next {
			next[i].atRoot = false
		}

		// 主题末层：过滤器 '#' 匹配其父层本身（含零层）。
		if depth == len(levels)-1 {
			for _, fr := range active {
				// 终止于该节点的精确过滤器与 '+' 过滤器恰好匹配整个主题。
				addSubs(fr.node, fr.prefix)
				if fr.node.hash != nil && !(fr.atRoot && rootBarsWild) {
					addSubs(fr.node.hash, fr.prefix+"/#")
				}
			}
		}
	}

	deliveries := make([]Delivery, 0, len(granted))
	for clientID, qos := range granted {
		deliveries = append(deliveries, Delivery{ClientID: clientID, GrantedQoS: qos})
	}
	sort.Slice(deliveries, func(i, j int) bool { return deliveries[i].ClientID < deliveries[j].ClientID })

	if retain {
		if payload == "" {
			delete(t.retained, topic)
		} else {
			t.retained[topic] = payload
		}
	}

	reasons := make([]string, 0, len(deliveries))
	for _, d := range deliveries {
		reasons = append(reasons, d.ClientID+"<-["+strings.Join(basis[d.ClientID], ",")+"]")
	}
	t.logf("PUBLISH ok topic=%q payloadLen=%d retain=%v deliveries=%d basis=%v retainedTopics=%d",
		topic, len(payload), retain, len(deliveries), reasons, len(t.retained))
	return deliveries, nil
}

func joinFilter(prefix, level string) string {
	if prefix == "" {
		return level
	}
	return prefix + "/" + level
}

func itoa(i int) string {
	return string(rune('0' + i))
}

// Retained 按主题查询保留消息；无保留（含已被空载荷清除）时 ok 为 false。
func (t *Table) Retained(topic string) (string, bool, error) {
	if _, err := validateTopic(topic); err != nil {
		return "", false, err
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	payload, ok := t.retained[topic]
	return payload, ok, nil
}
