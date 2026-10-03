package limiter

import (
	"testing"

	"ontology/window"
)

func counterOf(t *testing.T, l *Limiter, id string, values ...string) window.Snapshot {
	t.Helper()
	s, ok := l.table.Get(window.Key{RuleID: id, Values: values})
	if !ok {
		t.Fatalf("counter %s %v missing", id, values)
	}
	return s
}

func counterExists(l *Limiter, id string, values ...string) bool {
	_, ok := l.table.Get(window.Key{RuleID: id, Values: values})
	return ok
}

func dumpCounters(l *Limiter) map[string]window.Snapshot {
	out := map[string]window.Snapshot{}
	l.table.Range(func(sig string, s window.Snapshot) { out[sig] = s })
	return out
}

// 供外部测试包 limiter_test 读取内部状态的钩子。

func dumpCountersViaRange(l *Limiter, into map[string]window.Snapshot) map[string]window.Snapshot {
	l.table.Range(func(sig string, s window.Snapshot) { into[sig] = s })
	return into
}

func shadowSnapshot(l *Limiter) map[string]int64 {
	out := map[string]int64{}
	for k, v := range l.shadow {
		out[k] = v
	}
	return out
}
