package topicstore

import (
	"errors"
	"sort"
	"sync"
)

// 可区分的拒绝原因。
var (
	ErrInvalidFilter  = errors.New("invalid filter")
	ErrInvalidTopic   = errors.New("invalid publish topic")
	ErrInvalidQoS     = errors.New("qos out of range")
	ErrNoSubscription = errors.New("subscription does not exist")
)

// RetainedMessage 是订阅时随匹配下发的一条保留消息。
type RetainedMessage struct {
	Topic   string
	Payload string
}

// Delivery 表示一次发布命中的某个客户端及其授予等级。
type Delivery struct {
	ClientID   string
	GrantedQoS int
}

// SubscribeResult 是订阅操作的结果。
type SubscribeResult struct {
	Retained []RetainedMessage
}

// sub 是一条订阅的内部表示。
type sub struct {
	clientID string
	qos      int
	levels   []string
}

// Store 是层级主题通配订阅表。零值不可用，请用 New。
//
// 所有操作在同一把互斥锁下完成，因此并发调用的结果等价于某个串行
// 顺序；发布期间并发的订阅要么完整看到该次发布、要么完全看不到。
type Store struct {
	mu       sync.Mutex
	subs     []sub // 订阅条目，追加写入；退订时删除
	retained map[string]string
}

// New 创建一个空订阅表。
func New() *Store {
	return &Store{retained: make(map[string]string)}
}

// Subscribe 建立或覆盖订阅，并返回与过滤器匹配的保留消息。
func (s *Store) Subscribe(clientID, filter string, qos int) (SubscribeResult, error) {
	// 过滤器非法优先于等级越界报告。
	if !validateFilter(filter) {
		return SubscribeResult{}, ErrInvalidFilter
	}
	if qos < 0 || qos > 2 {
		return SubscribeResult{}, ErrInvalidQoS
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	levels := splitLevels(filter)
	for i := range s.subs {
		if s.subs[i].clientID == clientID && equalLevels(s.subs[i].levels, levels) {
			// 同一客户端重复订阅同一过滤器：仅覆盖等级。
			s.subs[i].qos = qos
			return SubscribeResult{Retained: s.matchedRetainedLocked(levels)}, nil
		}
	}
	s.subs = append(s.subs, sub{clientID: clientID, qos: qos, levels: levels})
	return SubscribeResult{Retained: s.matchedRetainedLocked(levels)}, nil
}

// Unsubscribe 退订；不存在时返回 ErrNoSubscription。
func (s *Store) Unsubscribe(clientID, filter string) error {
	if !validateFilter(filter) {
		return ErrInvalidFilter
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	levels := splitLevels(filter)
	for i := range s.subs {
		if s.subs[i].clientID == clientID && equalLevels(s.subs[i].levels, levels) {
			s.subs = append(s.subs[:i], s.subs[i+1:]...)
			return nil
		}
	}
	return ErrNoSubscription
}

// Publish 按主题匹配订阅，返回去重合并后的投递列表；retained 为真时更新保留消息。
func (s *Store) Publish(topic, payload string, retained bool) ([]Delivery, error) {
	if !validateTopic(topic) {
		return nil, ErrInvalidTopic
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if retained {
		if payload == "" {
			// 空载荷清除该主题的保留消息。
			delete(s.retained, topic)
		} else {
			s.retained[topic] = payload
		}
	}

	tLevels := splitLevels(topic)
	// 同一客户端多个过滤器命中时只出现一次，等级取最大值。
	best := make(map[string]int)
	for _, sub := range s.subs {
		if matchLevels(sub.levels, tLevels) {
			if cur, ok := best[sub.clientID]; !ok || sub.qos > cur {
				best[sub.clientID] = sub.qos
			}
		}
	}
	deliveries := make([]Delivery, 0, len(best))
	for clientID, qos := range best {
		deliveries = append(deliveries, Delivery{ClientID: clientID, GrantedQoS: qos})
	}
	sort.Slice(deliveries, func(i, j int) bool {
		return deliveries[i].ClientID < deliveries[j].ClientID
	})
	return deliveries, nil
}

// matchedRetainedLocked 返回所有与给定过滤器匹配的保留消息，
// 按主题字节序升序。调用方需持有 s.mu。
func (s *Store) matchedRetainedLocked(filterLevels []string) []RetainedMessage {
	var out []RetainedMessage
	for topic, payload := range s.retained {
		if matchLevels(filterLevels, splitLevels(topic)) {
			out = append(out, RetainedMessage{Topic: topic, Payload: payload})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Topic < out[j].Topic })
	return out
}

func equalLevels(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
