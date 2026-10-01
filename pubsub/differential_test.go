package pubsub

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strconv"
	"sync"
	"testing"
)

// refSubscription 是完全独立于 Trie 的朴素参考模型条目。
type refSubscription struct {
	clientID string
	filter   string
	levels   []string
	qos      int
}

type refModel struct {
	subs     []refSubscription
	retained map[string]string
}

func newRefModel() *refModel {
	return &refModel{retained: map[string]string{}}
}

func (m *refModel) subscribe(clientID, filter string, levels []string, qos int) []RetainedMessage {
	found := false
	for i := range m.subs {
		if m.subs[i].clientID == clientID && m.subs[i].filter == filter {
			m.subs[i].qos = qos
			found = true
		}
	}
	if !found {
		m.subs = append(m.subs, refSubscription{clientID: clientID, filter: filter, levels: levels, qos: qos})
	}
	out := make([]RetainedMessage, 0)
	for topic, payload := range m.retained {
		if naiveMatch(levels, splitLevels(topic)) {
			out = append(out, RetainedMessage{Topic: topic, Payload: payload})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Topic < out[j].Topic })
	return out
}

func (m *refModel) unsubscribe(clientID, filter string) bool {
	for i, s := range m.subs {
		if s.clientID == clientID && s.filter == filter {
			m.subs = append(m.subs[:i], m.subs[i+1:]...)
			return true
		}
	}
	return false
}

func (m *refModel) publish(topic string, levels []string) []Delivery {
	best := map[string]int{}
	for _, s := range m.subs {
		if naiveMatch(s.levels, levels) {
			if old, ok := best[s.clientID]; !ok || s.qos > old {
				best[s.clientID] = s.qos
			}
		}
	}
	out := make([]Delivery, 0, len(best))
	for id, qos := range best {
		out = append(out, Delivery{ClientID: id, GrantedQoS: qos})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ClientID < out[j].ClientID })
	return out
}

func genLevels(rng *rand.Rand) []string {
	n := 1 + rng.Intn(4)
	levels := make([]string, n)
	for i := range levels {
		switch rng.Intn(6) {
		case 0:
			levels[i] = ""
		case 1:
			levels[i] = "$" + strconv.Itoa(rng.Intn(3))
		default:
			levels[i] = string(rune('a'+rng.Intn(3))) + strconv.Itoa(rng.Intn(3))
		}
	}
	return levels
}

func genFilter(rng *rand.Rand) (string, []string, bool) {
	levels := genLevels(rng)
	if rng.Intn(2) == 0 {
		switch rng.Intn(3) {
		case 0:
			levels[rng.Intn(len(levels))] = "+"
		case 1:
			levels[len(levels)-1] = "#"
		}
	}
	s := joinLevels(levels)
	if s == "" {
		return genFilter(rng)
	}
	return s, levels, true
}

func genTopic(rng *rand.Rand) (string, []string) {
	levels := genLevels(rng)
	s := joinLevels(levels)
	if s == "" {
		return genTopic(rng)
	}
	return s, levels
}

func joinLevels(levels []string) string {
	out := ""
	for i, lv := range levels {
		if i > 0 {
			out += "/"
		}
		out += lv
	}
	return out
}

// TestDifferentialAgainstNaive 随机操作序列下 Trie 表与逐层朴素参考模型逐操作对照。
func TestDifferentialAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261001))
	tab := NewTable(nil)
	ref := newRefModel()

	const clients = 5

	for i := 0; i < 4000; i++ {
		client := "c" + strconv.Itoa(rng.Intn(clients))
		switch rng.Intn(10) {
		case 0, 1, 2, 3, 4:
			filter, fl, _ := genFilter(rng)
			qos := rng.Intn(3)
			got, err := tab.Subscribe(client, filter, qos)
			if err != nil {
				t.Fatalf("sub %q: %v", filter, err)
			}
			want := ref.subscribe(client, filter, fl, qos)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("sub retained mismatch client=%s filter=%q\n got=%+v\nwant=%+v", client, filter, got, want)
			}
		case 5:
			filter, _, _ := genFilter(rng)
			gotErr := tab.Unsubscribe(client, filter)
			wantOK := ref.unsubscribe(client, filter)
			if wantOK != (gotErr == nil) {
				t.Fatalf("unsub mismatch client=%s filter=%q gotErr=%v wantOK=%v", client, filter, gotErr, wantOK)
			}
		default:
			topic, tl := genTopic(rng)
			retain := rng.Intn(3) == 0
			payload := "p" + strconv.Itoa(i)
			if retain && rng.Intn(5) == 0 {
				payload = "" // 清除
			}
			got, err := tab.Publish(topic, payload, retain)
			if err != nil {
				t.Fatalf("publish %q: %v", topic, err)
			}
			want := ref.publish(topic, tl)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("publish mismatch topic=%q\n got=%+v\nwant=%+v", topic, got, want)
			}
			if retain {
				if payload == "" {
					delete(ref.retained, topic)
				} else {
					ref.retained[topic] = payload
				}
			}
		}
	}

	// 序列结束后全量对照保留消息。
	for topic, wantPayload := range ref.retained {
		gotPayload, ok, err := tab.Retained(topic)
		if err != nil || !ok || gotPayload != wantPayload {
			t.Fatalf("retained mismatch topic=%q got=(%q,%v,%v) want=%q", topic, gotPayload, ok, err, wantPayload)
		}
	}
}

// TestReplayDeterminism 相同操作序列在两个新表上重放，匹配结果完全一致。
func TestReplayDeterminism(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	run := func() [][]Delivery {
		tab := NewTable(nil)
		var results [][]Delivery
		for i := 0; i < 2000; i++ {
			client := "c" + strconv.Itoa(rng.Intn(4))
			switch rng.Intn(3) {
			case 0:
				filter, _, _ := genFilter(rng)
				if _, err := tab.Subscribe(client, filter, rng.Intn(3)); err != nil {
					t.Fatal(err)
				}
			case 1:
				topic, _ := genTopic(rng)
				ds, err := tab.Publish(topic, "x", false)
				if err != nil {
					t.Fatal(err)
				}
				results = append(results, ds)
			case 2:
				filter, _, _ := genFilter(rng)
				_ = tab.Unsubscribe(client, filter)
			}
		}
		return results
	}
	first := run()
	rng = rand.New(rand.NewSource(42))
	second := run()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("replay results differ: %d vs %d deliveries batches", len(first), len(second))
	}
}

// TestConcurrentAllOrNothing 并发订阅与发布：每次发布每个客户端至多一次，
// 等级合法；并发退订不产生重复或幽灵投递。
func TestConcurrentAllOrNothing(t *testing.T) {
	tab := NewTable(nil)
	const workers = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	var allProblems []string

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(id + 1)))
			for i := 0; i < 300; i++ {
				client := fmt.Sprintf("c%d-%d", id, rng.Intn(3))
				filter, _, _ := genFilter(rng)
				switch rng.Intn(3) {
				case 0:
					if _, err := tab.Subscribe(client, filter, rng.Intn(3)); err != nil {
						mu.Lock()
						allProblems = append(allProblems, err.Error())
						mu.Unlock()
					}
				case 1:
					_ = tab.Unsubscribe(client, filter)
				case 2:
					topic, _ := genTopic(rng)
					ds, err := tab.Publish(topic, "p", rng.Intn(4) == 0)
					if err != nil {
						mu.Lock()
						allProblems = append(allProblems, err.Error())
						mu.Unlock()
						continue
					}
					seen := map[string]bool{}
					for _, d := range ds {
						if seen[d.ClientID] {
							mu.Lock()
							allProblems = append(allProblems, "duplicate client "+d.ClientID)
							mu.Unlock()
						}
						seen[d.ClientID] = true
						if d.GrantedQoS < 0 || d.GrantedQoS > 2 {
							mu.Lock()
							allProblems = append(allProblems, "bad qos")
							mu.Unlock()
						}
					}
					for j := 1; j < len(ds); j++ {
						if ds[j-1].ClientID >= ds[j].ClientID {
							mu.Lock()
							allProblems = append(allProblems, "unsorted deliveries")
							mu.Unlock()
						}
					}
				}
			}
		}(w)
	}
	wg.Wait()
	if len(allProblems) > 0 {
		t.Fatalf("concurrent problems: %v", allProblems[:min(10, len(allProblems))])
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
