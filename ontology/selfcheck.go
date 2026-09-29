package ontology

import "fmt"

// SelfCheck 校验视图内部不变量；发现损坏时返回描述性错误。
func (v *MaterializedView) SelfCheck() error {
	v.mu.RLock()
	defer v.mu.RUnlock()

	if v.maxKeys <= 0 {
		return fmt.Errorf("ontology self-check: maxKeys must be positive, got %d", v.maxKeys)
	}

	live := 0
	for key, cur := range v.state {
		if !cur.seen {
			return fmt.Errorf("ontology self-check: key %q has unseen entry", key)
		}
		if cur.eventTime < 0 {
			return fmt.Errorf("ontology self-check: key %q has negative event time %d", key, cur.eventTime)
		}
		if !cur.exists && cur.value != "" {
			return fmt.Errorf("ontology self-check: absent key %q retains value %q", key, cur.value)
		}
		if cur.exists {
			live++
		}
	}
	if live > v.maxKeys {
		return fmt.Errorf("ontology self-check: live key count %d exceeds limit %d", live, v.maxKeys)
	}

	// 变更日志：序列号必须从 1 起连续严格递增，且可完整重放出当前状态。
	replayed := make(map[string]*entry, len(v.state))
	causes := int64(0)
	for i, ch := range v.log {
		if ch.Seq != int64(i+1) {
			return fmt.Errorf("ontology self-check: change at index %d has seq %d, want %d", i, ch.Seq, i+1)
		}
		if ch.Key == "" {
			return fmt.Errorf("ontology self-check: change at index %d has empty key", i)
		}
		cur := replayed[ch.Key]
		if cur == nil {
			cur = &entry{}
			replayed[ch.Key] = cur
		}
		switch ch.Kind {
		case ChangeRetract:
			if !cur.exists {
				return fmt.Errorf("ontology self-check: retract for key %q at seq %d without existing value", ch.Key, ch.Seq)
			}
			if cur.value != ch.OldValue || cur.eventTime != ch.OldEventTime {
				return fmt.Errorf("ontology self-check: retract at seq %d mismatches prior state of key %q", ch.Seq, ch.Key)
			}
			cur.exists = false
		case ChangeEstablish:
			cur.exists = true
			cur.value = ch.NewValue
			cur.eventTime = ch.NewEventTime
			cur.seen = true
			causes++
		case ChangeTombstone:
			cur.exists = false
			cur.value = ""
			cur.eventTime = ch.NewEventTime
			cur.seen = true
			causes++
		default:
			return fmt.Errorf("ontology self-check: change at seq %d has unknown kind %d", ch.Seq, ch.Kind)
		}
	}

	if causes != v.applied {
		return fmt.Errorf("ontology self-check: applied counter %d != establish/tombstone causes %d", v.applied, causes)
	}

	if len(replayed) != len(v.state) {
		return fmt.Errorf("ontology self-check: replayed key count %d != state key count %d", len(replayed), len(v.state))
	}
	for key, want := range v.state {
		got := replayed[key]
		if got == nil {
			return fmt.Errorf("ontology self-check: key %q missing from replayed log", key)
		}
		if got.seen != want.seen || got.exists != want.exists ||
			got.value != want.value || got.eventTime != want.eventTime {
			return fmt.Errorf("ontology self-check: replayed state for key %q = %+v, want %+v", key, *got, *want)
		}
	}
	return nil
}
