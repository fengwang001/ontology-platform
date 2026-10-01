package topicstore

import (
	"errors"
	"math/rand"
	"strings"
	"sync"
	"testing"
)

// naiveMatch 是测试专用的、独立于生产实现的“逐层朴素匹配”：
// 用递归方式逐层消费 filter/topic，按题目规则直接书写，
// 用来与生产代码 matchLevels 交叉对照。
func naiveMatch(filter, topic []string) bool {
	// "$" 主题例外：首层为 "+" 或 "#" 的过滤器不匹配首层以 "$" 开头的主题。
	if len(topic) > 0 && strings.HasPrefix(topic[0], "$") &&
		len(filter) > 0 && (filter[0] == "+" || filter[0] == "#") {
		return false
	}
	var rec func(fi, ti int) bool
	rec = func(fi, ti int) bool {
		switch {
		case fi == len(filter):
			// 过滤器耗尽：主题也必须恰好耗尽（"#" 已在前面分支处理）。
			return ti == len(topic)
		case filter[fi] == "#":
			// "#" 必为末层：匹配父层（零层）及其下任意多层。
			return fi == len(filter)-1
		case ti == len(topic):
			// 主题已耗尽而过滤器还有非 "#" 层级：不匹配。
			return false
		case filter[fi] == "+":
			// 恰好一层，空层也算一层。
			return rec(fi+1, ti+1)
		default:
			// 字面值必须完全相同。
			return filter[fi] == topic[ti] && rec(fi+1, ti+1)
		}
	}
	return rec(0, 0)
}

func TestMatchRulesTable(t *testing.T) {
	cases := []struct {
		filter string
		topic  string
		want   bool
		reason string
	}{
		{"a/#", "a", true, "# 匹配父层自身（零层）"},
		{"a/#", "a/b", true, "# 匹配父层下一层"},
		{"a/#", "a/b/c", true, "# 匹配父层下任意多层"},
		{"a/#", "b", false, "父层字面不同"},
		{"a/#", "a1", false, "层级前缀不等于父层"},
		{"+", "a", true, "+ 匹配恰好一层"},
		{"+//b", "x//b", true, "+ 匹配一层，中间空层逐字相等"},
		{"a/+/b", "a//b", true, "+ 匹配空层"},
		{"a/+/b", "a/x/b", true, "+ 匹配非空层"},
		{"a/+/b", "a/x/x/b", false, "+ 只匹配恰好一层"},
		{"/a", "/a", true, "首层为空的字面值匹配"},
		{"#", "a/b/c", true, "单独 # 匹配任意多层"},
		{"#", "a", true, "单独 # 匹配单层"},
		{"#", "$SYS/x", false, "# 不匹配 $ 开头主题"},
		{"+", "$SYS", false, "+ 不匹配 $ 开头主题"},
		{"$SYS/#", "$SYS/x", true, "写明 $ 字面值的过滤器正常匹配"},
		{"$SYS/+", "$SYS/x", true, "写明 $ 字面值后 + 仍匹配下一层"},
		{"a/b", "a/b", true, "纯字面值相等"},
		{"a/b", "a/c", false, "纯字面值末层不同"},
	}
	for _, c := range cases {
		got := match(c.filter, c.topic)
		naive := naiveMatch(splitLevels(c.filter), splitLevels(c.topic))
		t.Logf("输入 filter=%q topic=%q => 生产匹配=%v 朴素匹配=%v（判定依据：%s）",
			c.filter, c.topic, got, naive, c.reason)
		if got != c.want || naive != c.want {
			t.Errorf("filter=%q topic=%q got=%v naive=%v want=%v（%s）",
				c.filter, c.topic, got, naive, c.want, c.reason)
		}
	}
}

func TestNaiveCrossCheckRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	literals := []string{"a", "b", "$SYS", ""}
	pick := func(allowWild, last bool) string {
		if allowWild && last && rng.Intn(4) == 0 {
			return "#"
		}
		if allowWild && rng.Intn(3) == 0 {
			return "+"
		}
		return literals[rng.Intn(len(literals))]
	}
	gen := func(maxLevels int, allowWild bool) []string {
		n := rng.Intn(maxLevels) + 1
		out := make([]string, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, pick(allowWild, i == n-1))
		}
		return out
	}
	checked := 0
	for i := 0; i < 20000; i++ {
		fRaw := gen(4, true)
		f := strings.Join(fRaw, "/")
		if !validateFilter(f) {
			continue
		}
		topicRaw := gen(4, false)
		topic := strings.Join(topicRaw, "/")
		if !validateTopic(topic) {
			continue
		}
		got := matchLevels(fRaw, topicRaw)
		want := naiveMatch(fRaw, topicRaw)
		if got != want {
			t.Fatalf("分歧 filter=%q topic=%q 生产=%v 朴素=%v", f, topic, got, want)
		}
		checked++
	}
	t.Logf("随机交叉对照完成：%d 组合法 (过滤器, 主题) 生产实现与逐层朴素匹配完全一致", checked)
}

func TestInvalidFiltersAndTopics(t *testing.T) {
	badFilters := []string{"", "a/#/b", "#/a", "a+/b", "a/+b", "a/b#"}
	for _, f := range badFilters {
		if validateFilter(f) {
			t.Errorf("过滤器应判非法: %q", f)
		}
		t.Logf("过滤器 %q 判定为非法（空串 / # 非末层 / 通配符与其他字符同层）", f)
	}
	goodFilters := []string{"#", "+", "a/#", "+/b", "a/+/c/#", "/+", "a//+"}
	for _, f := range goodFilters {
		if !validateFilter(f) {
			t.Errorf("过滤器应判合法: %q", f)
		}
		t.Logf("过滤器 %q 判定为合法", f)
	}
	badTopics := []string{"", "a/+/b", "a/#", "+", "#"}
	for _, topic := range badTopics {
		if validateTopic(topic) {
			t.Errorf("主题应判非法: %q", topic)
		}
		t.Logf("发布主题 %q 判定为非法（空串或含通配字符）", topic)
	}
}

func TestRejectedOperationsDoNotMutate(t *testing.T) {
	s := New()
	if _, err := s.Subscribe("c1", "", 0); !errors.Is(err, ErrInvalidFilter) {
		t.Fatalf("空过滤器: got %v want ErrInvalidFilter", err)
	}
	// 过滤器非法与等级越界同时成立时报过滤器非法。
	if _, err := s.Subscribe("c1", "a+/b", 9); !errors.Is(err, ErrInvalidFilter) {
		t.Fatalf("优先级: got %v want ErrInvalidFilter", err)
	}
	if _, err := s.Subscribe("c1", "a/b", 3); !errors.Is(err, ErrInvalidQoS) {
		t.Fatalf("等级越界: got %v want ErrInvalidQoS", err)
	}
	if _, err := s.Subscribe("c1", "a/b", -1); !errors.Is(err, ErrInvalidQoS) {
		t.Fatalf("等级越界: got %v want ErrInvalidQoS", err)
	}
	if err := s.Unsubscribe("c1", "a/b"); !errors.Is(err, ErrNoSubscription) {
		t.Fatalf("退订不存在: got %v want ErrNoSubscription", err)
	}
	if _, err := s.Publish("", "x", false); !errors.Is(err, ErrInvalidTopic) {
		t.Fatalf("空主题: got %v want ErrInvalidTopic", err)
	}
	if _, err := s.Publish("a/+/b", "x", false); !errors.Is(err, ErrInvalidTopic) {
		t.Fatalf("含通配符主题: got %v want ErrInvalidTopic", err)
	}
	// 被拒绝的操作均未改变状态：合法订阅后只应看到合法订阅者，保留表为空。
	if _, err := s.Subscribe("ok", "#", 0); err != nil {
		t.Fatal(err)
	}
	got, err := s.Publish("x", "y", false)
	if err != nil {
		t.Fatal(err)
	}
	wantD := []Delivery{{ClientID: "ok", GrantedQoS: 0}}
	if !deliveriesEqual(got, wantD) {
		t.Fatalf("拒绝后状态异常: got %v want %v", got, wantD)
	}
	res, err := s.Subscribe("probe", "#", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Retained) != 0 {
		t.Fatalf("被拒绝的保留发布不得写入保留消息: %v", res.Retained)
	}
	t.Logf("全部拒绝用例返回可区分错误且未改变状态；合法发布输出=%v 探测保留=%v", got, res.Retained)
}

func mustSub(t *testing.T, s *Store, clientID, filter string, qos int) {
	t.Helper()
	if _, err := s.Subscribe(clientID, filter, qos); err != nil {
		t.Fatalf("Subscribe(%q,%q,%d): %v", clientID, filter, qos, err)
	}
}

func TestDuplicateSubscribeOverridesQoS(t *testing.T) {
	s := New()
	mustSub(t, s, "c1", "a/b", 0)
	mustSub(t, s, "c1", "a/b", 2) // 同客户端同过滤器：覆盖等级，不新增条目
	mustSub(t, s, "c2", "a/b", 1)

	got, err := s.Publish("a/b", "p", false)
	if err != nil {
		t.Fatal(err)
	}
	wantD := []Delivery{{ClientID: "c1", GrantedQoS: 2}, {ClientID: "c2", GrantedQoS: 1}}
	t.Logf("输入: c1 重复订阅 a/b(0->2), c2 订阅 a/b(1)；发布 a/b => 输出 %v（重复订阅只覆盖等级，按 ID 升序）", got)
	if !deliveriesEqual(got, wantD) {
		t.Fatalf("got %v want %v", got, wantD)
	}
}

func TestMultipleFiltersMergeMaxQoS(t *testing.T) {
	s := New()
	mustSub(t, s, "c1", "a/+", 0)
	mustSub(t, s, "c1", "a/#", 2)
	mustSub(t, s, "c1", "+/b", 1)
	mustSub(t, s, "c2", "#", 1)

	got, err := s.Publish("a/b", "p", false)
	if err != nil {
		t.Fatal(err)
	}
	wantD := []Delivery{{ClientID: "c1", GrantedQoS: 2}, {ClientID: "c2", GrantedQoS: 1}}
	t.Logf("输入: c1 的 a/+、a/#、+/b（等级 0/2/1）均命中 a/b => 输出 %v（每客户端一次，取最大等级）", got)
	if !deliveriesEqual(got, wantD) {
		t.Fatalf("got %v want %v", got, wantD)
	}

	// 退订其中一个过滤器后，其余过滤器仍命中，等级重新取最大值。
	if err := s.Unsubscribe("c1", "a/#"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Publish("a/b", "p", false)
	wantD = []Delivery{{ClientID: "c1", GrantedQoS: 1}, {ClientID: "c2", GrantedQoS: 1}}
	t.Logf("退订 c1 的 a/# 后发布 a/b => 输出 %v（剩余等级 0 与 1，最大为 1）", got)
	if !deliveriesEqual(got, wantD) {
		t.Fatalf("got %v want %v", got, wantD)
	}
}

func TestRetainedOverwriteClearAndDeliver(t *testing.T) {
	s := New()

	// 发布两条保留消息。
	if d, err := s.Publish("a/1", "v1", true); err != nil || len(d) != 0 {
		t.Fatalf("保留发布时尚无订阅: d=%v err=%v", d, err)
	}
	if _, err := s.Publish("a/2", "v2", true); err != nil {
		t.Fatal(err)
	}

	// 新订阅按主题字节序收到全部匹配的保留消息。
	res, err := s.Subscribe("late", "a/#", 1)
	if err != nil {
		t.Fatal(err)
	}
	wantRet := []RetainedMessage{{Topic: "a/1", Payload: "v1"}, {Topic: "a/2", Payload: "v2"}}
	t.Logf("输入: 已有保留 a/1=v1,a/2=v2；订阅 a/# => 输出 %v（按主题字节序）", res.Retained)
	if !retainedEqual(res.Retained, wantRet) {
		t.Fatalf("got %v want %v", res.Retained, wantRet)
	}

	// 覆盖：同主题只保留最后一条。
	if _, err := s.Publish("a/1", "v1-new", true); err != nil {
		t.Fatal(err)
	}
	res, _ = s.Subscribe("late2", "a/1", 0)
	t.Logf("覆盖后订阅 a/1 => %v（同主题只留最后一条）", res.Retained)
	if !retainedEqual(res.Retained, []RetainedMessage{{Topic: "a/1", Payload: "v1-new"}}) {
		t.Fatalf("覆盖失败: %v", res.Retained)
	}

	// 清除：空载荷的保留发布删除该主题。
	if _, err := s.Publish("a/1", "", true); err != nil {
		t.Fatal(err)
	}
	res, _ = s.Subscribe("late3", "a/#", 0)
	t.Logf("空载荷清除 a/1 后订阅 a/# => %v（a/1 已删除）", res.Retained)
	if !retainedEqual(res.Retained, []RetainedMessage{{Topic: "a/2", Payload: "v2"}}) {
		t.Fatalf("清除失败: %v", res.Retained)
	}

	// "$" 例外同样适用于保留消息下发。
	if _, err := s.Publish("$SYS/health", "ok", true); err != nil {
		t.Fatal(err)
	}
	res, _ = s.Subscribe("wild", "#", 0)
	t.Logf("订阅 # 的保留下发 => %v（不含 $ 开头主题）", res.Retained)
	for _, m := range res.Retained {
		if strings.HasPrefix(m.Topic, "$") {
			t.Fatalf("# 不应下发 $ 开头的保留消息: %v", m)
		}
	}
	res, _ = s.Subscribe("sys", "$SYS/#", 0)
	t.Logf("订阅 $SYS/# 的保留下发 => %v（写明 $ 字面值可命中）", res.Retained)
	if !retainedEqual(res.Retained, []RetainedMessage{{Topic: "$SYS/health", Payload: "ok"}}) {
		t.Fatalf("$ 保留消息下发异常: %v", res.Retained)
	}
}

func TestReplayDeterminism(t *testing.T) {
	// 相同调用序列在两个独立实例上重放，匹配结果必须完全一致。
	play := func() []string {
		s := New()
		log := make([]string, 0)
		ops := []func(){
			func() { mustSub(t, s, "c1", "a/+", 0) },
			func() { mustSub(t, s, "c2", "a/#", 2) },
			func() { mustSub(t, s, "c3", "#", 1) },
			func() { _, _ = s.Publish("a/b", "p1", true) },
			func() { mustSub(t, s, "c1", "a/b", 2) },
		}
		for _, op := range ops {
			op()
		}
		for _, topic := range []string{"a", "a/b", "a/b/c", "$SYS/x"} {
			d, err := s.Publish(topic, "p", false)
			if err != nil {
				t.Fatal(err)
			}
			log = append(log, formatDeliveries(topic, d))
		}
		res, _ := s.Subscribe("late", "#", 0)
		for _, m := range res.Retained {
			log = append(log, "retain:"+m.Topic+"="+m.Payload)
		}
		return log
	}
	first := play()
	for i := 0; i < 5; i++ {
		again := play()
		if len(again) != len(first) {
			t.Fatalf("重放长度不一致")
		}
		for j := range first {
			if again[j] != first[j] {
				t.Fatalf("重放结果不确定:\nfirst: %v\nagain: %v", first, again)
			}
		}
	}
	t.Logf("相同序列重放 6 次结果完全一致:\n  %s", strings.Join(first, "\n  "))
}

func TestConcurrentPublishSubscribe(t *testing.T) {
	// 并发不变量：
	//  1. 每次发布中，每个客户端至多出现一次；
	//  2. 发布期间并发订阅的客户端要么完整看到该次发布、要么完全看不到
	//     （单把互斥锁使每次调用原子，订阅者一旦出现必带确定等级）。
	s := New()
	filters := []string{"a/+", "a/#", "+/b", "#", "a/b", "$SYS/#"}
	topics := []string{"a", "a/b", "a/c", "a/b/c", "$SYS/x"}

	var wg sync.WaitGroup
	for c := 0; c < 12; c++ {
		clientID := "c" + itoa(c)
		wg.Add(1)
		go func() {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(c)))
			for i := 0; i < 200; i++ {
				f := filters[rng.Intn(len(filters))]
				qos := rng.Intn(3)
				if _, err := s.Subscribe(clientID, f, qos); err != nil {
					t.Errorf("subscribe: %v", err)
					return
				}
				if rng.Intn(4) == 0 {
					if err := s.Unsubscribe(clientID, f); err != nil &&
						!errors.Is(err, ErrNoSubscription) {
						t.Errorf("unsubscribe: %v", err)
						return
					}
				}
			}
		}()
	}
	for p := 0; p < 8; p++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 300; i++ {
				topic := topics[rng.Intn(len(topics))]
				d, err := s.Publish(topic, "x", rng.Intn(5) == 0)
				if err != nil {
					t.Errorf("publish: %v", err)
					return
				}
				seen := make(map[string]int)
				var prev string
				for _, dl := range d {
					if _, dup := seen[dl.ClientID]; dup {
						t.Errorf("客户端 %s 在同一次发布中出现多次: %v", dl.ClientID, d)
						return
					}
					if dl.ClientID <= prev && prev != "" {
						t.Errorf("投递未按客户端标识升序: %v", d)
						return
					}
					if dl.GrantedQoS < 0 || dl.GrantedQoS > 2 {
						t.Errorf("授予等级越界: %v", d)
						return
					}
					seen[dl.ClientID] = dl.GrantedQoS
					prev = dl.ClientID
				}
			}
		}(int64(100 + p))
	}
	wg.Wait()
	t.Logf("12 个订阅者与 8 个发布者并发完成：去重、升序、等级范围不变量均成立")
}

func deliveriesEqual(got, want []Delivery) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func retainedEqual(got, want []RetainedMessage) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func formatDeliveries(topic string, d []Delivery) string {
	parts := make([]string, 0, len(d))
	for _, dl := range d {
		parts = append(parts, dl.ClientID+":q"+itoa(dl.GrantedQoS))
	}
	return topic + " -> [" + strings.Join(parts, ",") + "]"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
