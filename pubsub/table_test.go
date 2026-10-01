package pubsub

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func mustSub(t *testing.T, tab *Table, clientID, filter string, qos int) []RetainedMessage {
	t.Helper()
	msgs, err := tab.Subscribe(clientID, filter, qos)
	if err != nil {
		t.Fatalf("subscribe %s %s: %v", clientID, filter, err)
	}
	return msgs
}

// TestValidation 覆盖过滤器与主题的全部拒绝情形及优先级。
func TestValidation(t *testing.T) {
	tab := NewTable(nil)

	for _, f := range []string{"", "a/#/b", "#/a", "a+", "a/b#/c", "##", "x#", "+a", "a/+x", "a/#x"} {
		if _, err := tab.Subscribe("c", f, 0); !errors.Is(err, ErrInvalidFilter) {
			t.Fatalf("filter %q: want ErrInvalidFilter, got %v", f, err)
		}
		if err := tab.Unsubscribe("c", f); !errors.Is(err, ErrInvalidFilter) {
			t.Fatalf("unsubscribe filter %q: want ErrInvalidFilter, got %v", f, err)
		}
	}

	if _, err := tab.Subscribe("c", "a+", 9); !errors.Is(err, ErrInvalidFilter) {
		t.Fatalf("priority: want ErrInvalidFilter, got %v", err)
	}
	for _, qos := range []int{-1, 3, 42} {
		if _, err := tab.Subscribe("c", "+", qos); !errors.Is(err, ErrInvalidQoS) {
			t.Fatalf("qos %d: want ErrInvalidQoS, got %v", qos, err)
		}
	}

	for _, topic := range []string{"", "a/+", "a/#", "+", "#", "a/b+/c", "x#"} {
		if _, err := tab.Publish(topic, "p", false); !errors.Is(err, ErrInvalidTopic) {
			t.Fatalf("topic %q: want ErrInvalidTopic, got %v", topic, err)
		}
		if _, _, err := tab.Retained(topic); !errors.Is(err, ErrInvalidTopic) {
			t.Fatalf("retained topic %q: want ErrInvalidTopic, got %v", topic, err)
		}
	}
}

// TestWildcardRules 覆盖 a/# 匹配 a、'+' 匹配空层、'$' 例外等核心规则。
func TestWildcardRules(t *testing.T) {
	cases := []struct {
		filter, topic string
		want          bool
	}{
		{"#", "a", true},
		{"#", "a/b/c", true},
		{"#", "$SYS/x", false},
		{"#", "$", false},
		{"a/#", "a", true},
		{"a/#", "a/b", true},
		{"a/#", "a/b/c", true},
		{"a/#", "b", false},
		{"a/#", "b/a", false},
		{"a/+", "a", false},
		{"a/+", "a/b", true},
		{"a/+", "a/b/c", false},
		{"+", "a", true},
		{"+", "$SYS", false},
		{"$SYS/#", "$SYS", true},
		{"$SYS/#", "$SYS/a/b", true},
		{"$SYS/+", "$SYS/a", true},
		{"$SYS/+", "$SYS/a/b", false},
		{"+/x", "$SYS/x", false},
		{"$SYS/#", "other/x", false},
		{"a//b", "a//b", true},
		{"+/+", "a/", true},
		{"+/+", "/a", true},
		{"a/+", "a/", true},
		{"/+", "/a", true},
		{"/#", "/a", true},
		{"sport/tennis/+", "sport/tennis/player1", true},
		{"sport/tennis/+", "sport/tennis/player1/ranking", false},
		{"sport/#", "sport", true},
		{"a/b/c", "a/b/c", true},
		{"a/b/c", "a/b/d", false},
		{"a//b", "a/x/b", false},
	}
	for _, tc := range cases {
		fl, err := validateFilter(tc.filter)
		if err != nil {
			t.Fatalf("filter %q unexpectedly invalid: %v", tc.filter, err)
		}
		tl, err := validateTopic(tc.topic)
		if err != nil {
			t.Fatalf("topic %q unexpectedly invalid: %v", tc.topic, err)
		}
		if got := naiveMatch(fl, tl); got != tc.want {
			t.Errorf("naiveMatch(%q,%q)=%v want %v", tc.filter, tc.topic, got, tc.want)
		}
	}
}

// TestDuplicateSubscription 同一客户端重复订阅同一过滤器只覆盖等级。
func TestDuplicateSubscription(t *testing.T) {
	tab := NewTable(nil)
	mustSub(t, tab, "c1", "a/b", 0)
	mustSub(t, tab, "c1", "a/b", 2)
	ds, err := tab.Publish("a/b", "p", false)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ds, []Delivery{{ClientID: "c1", GrantedQoS: 2}}) {
		t.Fatalf("got %+v", ds)
	}

	mustSub(t, tab, "c1", "a/b", 1)
	ds, _ = tab.Publish("a/b", "p", false)
	if !reflect.DeepEqual(ds, []Delivery{{ClientID: "c1", GrantedQoS: 1}}) {
		t.Fatalf("got %+v", ds)
	}

	if err := tab.Unsubscribe("c1", "a/nonexistent"); !errors.Is(err, ErrNoSubscription) {
		t.Fatalf("want ErrNoSubscription, got %v", err)
	}
	if err := tab.Unsubscribe("nobody", "a/b"); !errors.Is(err, ErrNoSubscription) {
		t.Fatalf("want ErrNoSubscription, got %v", err)
	}
	if err := tab.Unsubscribe("c1", "a/b"); err != nil {
		t.Fatal(err)
	}
	if err := tab.Unsubscribe("c1", "a/b"); !errors.Is(err, ErrNoSubscription) {
		t.Fatalf("second unsubscribe: want ErrNoSubscription, got %v", err)
	}
}

// TestMaxQoSMerge 多过滤器命中同一客户端只投递一次且等级取最大，按客户端标识升序。
func TestMaxQoSMerge(t *testing.T) {
	tab := NewTable(nil)
	mustSub(t, tab, "c1", "a/#", 0)
	mustSub(t, tab, "c1", "a/+", 1)
	mustSub(t, tab, "c1", "a/b", 2)
	mustSub(t, tab, "c2", "a/#", 2)
	mustSub(t, tab, "c2", "a/+", 0)
	mustSub(t, tab, "c3", "x/#", 2)

	ds, err := tab.Publish("a/b", "p", false)
	if err != nil {
		t.Fatal(err)
	}
	want := []Delivery{
		{ClientID: "c1", GrantedQoS: 2},
		{ClientID: "c2", GrantedQoS: 2},
	}
	if !reflect.DeepEqual(ds, want) {
		t.Fatalf("got %+v, want %+v", ds, want)
	}

	mustSub(t, tab, "zeta", "a/b", 0)
	mustSub(t, tab, "alpha", "a/b", 0)
	mustSub(t, tab, "mid", "a/b", 0)
	ds, _ = tab.Publish("a/b", "p", false)
	gotIDs := make([]string, len(ds))
	for i, d := range ds {
		gotIDs[i] = d.ClientID
	}
	wantIDs := []string{"alpha", "c1", "c2", "mid", "zeta"}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("order got %v want %v", gotIDs, wantIDs)
	}
}

// TestRetained 保留消息覆盖、空载荷清除、订阅下发顺序与 '$' 例外。
func TestRetained(t *testing.T) {
	tab := NewTable(nil)

	if _, err := tab.Publish("a/1", "one", true); err != nil {
		t.Fatal(err)
	}
	if _, err := tab.Publish("a/2", "two", true); err != nil {
		t.Fatal(err)
	}
	if _, err := tab.Publish("a/1", "ONE", true); err != nil {
		t.Fatal(err)
	}
	msgs := mustSub(t, tab, "c1", "a/+", 1)
	want := []RetainedMessage{{Topic: "a/1", Payload: "ONE"}, {Topic: "a/2", Payload: "two"}}
	if !reflect.DeepEqual(msgs, want) {
		t.Fatalf("got %+v want %+v", msgs, want)
	}

	msgs, _ = tab.Subscribe("c2", "a/#", 2)
	if !reflect.DeepEqual(msgs, want) {
		t.Fatalf("got %+v", msgs)
	}

	if _, err := tab.Publish("a/1", "", true); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := tab.Retained("a/1"); err != nil || ok {
		t.Fatalf("a/1 should be cleared, ok=%v err=%v", ok, err)
	}
	msgs, _ = tab.Subscribe("c3", "a/+", 0)
	if !reflect.DeepEqual(msgs, []RetainedMessage{{Topic: "a/2", Payload: "two"}}) {
		t.Fatalf("got %+v", msgs)
	}

	if _, err := tab.Publish("a/2", "transient", false); err != nil {
		t.Fatal(err)
	}
	if p, ok, _ := tab.Retained("a/2"); !ok || p != "two" {
		t.Fatalf("retained changed by non-retain: %q ok=%v", p, ok)
	}

	if _, err := tab.Publish("$SYS/up", "1", true); err != nil {
		t.Fatal(err)
	}
	msgs, _ = tab.Subscribe("c4", "#", 0)
	for _, m := range msgs {
		if strings.HasPrefix(m.Topic, "$") {
			t.Fatalf("# must not deliver $ topic: %+v", msgs)
		}
	}
	msgs, _ = tab.Subscribe("c5", "$SYS/#", 0)
	if !reflect.DeepEqual(msgs, []RetainedMessage{{Topic: "$SYS/up", Payload: "1"}}) {
		t.Fatalf("got %+v", msgs)
	}

	if _, err := tab.Publish("a/3", "", true); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := tab.Retained("a/3"); ok {
		t.Fatal("empty payload on fresh topic must not create retained entry")
	}
}

// TestRejectedOpsNoSideEffect 被拒绝操作不得改变订阅表与保留消息。
func TestRejectedOpsNoSideEffect(t *testing.T) {
	tab := NewTable(nil)
	if _, err := tab.Subscribe("c1", "a/b", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := tab.Publish("a/b", "keep", true); err != nil {
		t.Fatal(err)
	}

	if _, err := tab.Subscribe("c1", "bad+", 0); !errors.Is(err, ErrInvalidFilter) {
		t.Fatal(err)
	}
	if _, err := tab.Subscribe("c1", "a/b", 3); !errors.Is(err, ErrInvalidQoS) {
		t.Fatal(err)
	}
	if err := tab.Unsubscribe("c1", "bad+"); !errors.Is(err, ErrInvalidFilter) {
		t.Fatal(err)
	}
	if err := tab.Unsubscribe("c1", "x/y"); !errors.Is(err, ErrNoSubscription) {
		t.Fatal(err)
	}
	if _, err := tab.Publish("", "x", true); !errors.Is(err, ErrInvalidTopic) {
		t.Fatal(err)
	}

	ds, _ := tab.Publish("a/b", "p", false)
	if !reflect.DeepEqual(ds, []Delivery{{ClientID: "c1", GrantedQoS: 1}}) {
		t.Fatalf("state changed by rejected ops: %+v", ds)
	}
	if p, ok, _ := tab.Retained("a/b"); !ok || p != "keep" {
		t.Fatalf("retained changed by rejected publish: %q %v", p, ok)
	}
}

// TestLogging 日志包含输入、输出与判定依据。
func TestLogging(t *testing.T) {
	var buf bytes.Buffer
	tab := NewTable(&buf)
	if _, err := tab.Subscribe("c1", "a/+", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := tab.Subscribe("c1", "a/#", 2); err != nil {
		t.Fatal(err)
	}
	if _, err := tab.Publish("a/b", "payload", true); err != nil {
		t.Fatal(err)
	}
	if _, err := tab.Subscribe("c2", "a/+", 1); err != nil {
		t.Fatal(err)
	}
	if err := tab.Unsubscribe("c1", "a/+"); err != nil {
		t.Fatal(err)
	}
	log := buf.String()
	for _, want := range []string{
		"SUBSCRIBE ok",
		`filter="a/+"`,
		"PUBLISH ok",
		`topic="a/b"`,
		"basis=",
		"c1<-[",
		"a/+@qos0",
		"a/#@qos2",
		"retained=1",
		"UNSUBSCRIBE ok",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("log missing %q\n%s", want, log)
		}
	}
}
