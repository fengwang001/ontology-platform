package notify

import (
	"sort"

	"ontology/alertstore"

	"ontology/suppress"
)

const (
	maxMillis     = int64(1_000_000_000_000)
	maxLabelPairs = 16
	maxMatcherNum = 8
	maxBytes      = 64
)

func validMillis(v int64) bool { return 0 <= v && v <= maxMillis }

func validLabels(l alertstore.Labels) bool {
	if len(l) < 1 || len(l) > maxLabelPairs {
		return false
	}
	for k, v := range l {
		if k == "" || v == "" || len(k) > maxBytes || len(v) > maxBytes {
			return false
		}
	}
	return true
}

func validMatcher(m suppress.Matcher) bool {
	if len(m) < 1 || len(m) > maxMatcherNum {
		return false
	}
	for k, v := range m {
		if k == "" || v == "" || len(k) > maxBytes || len(v) > maxBytes {
			return false
		}
	}
	return true
}

func validRule(r suppress.InhibitRule) bool {
	if !validMatcher(r.Source) || !validMatcher(r.Target) {
		return false
	}
	for _, name := range r.Equal {
		if name == "" || len(name) > maxBytes {
			return false
		}
	}
	return true
}

// checkClock 必须在状态类错误之前调用；它不推进时钟。
func (n *Notifier) checkClock(now int64) error {
	if n.hasTime && now < n.maxNow {
		return ErrClockRollback
	}
	return nil
}

func (n *Notifier) advanceClock(now int64) {
	if !n.hasTime || now > n.maxNow {
		n.maxNow = now
	}
	n.hasTime = true
}

// Tick 按分组节奏评估全部活跃告警，并在持锁状态下按组键序同步调用 send。
// send 为 nil 属参数非法；Tick 被接受即推进时钟，发送失败仅保留该组状态。
func (n *Notifier) Tick(now int64, send SendFunc) (TickResult, error) {
	if send == nil || !validMillis(now) {
		return TickResult{}, ErrInvalidArgument
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if err := n.checkClock(now); err != nil {
		return TickResult{}, err
	}
	n.advanceClock(now)
	alerts := n.store.Snapshot()
	firing := map[string]*alertstore.Alert{}
	groups := map[string][]*alertstore.Alert{}
	for _, a := range alerts {
		if a.Firing {
			firing[a.Fingerprint] = a
		}
		k := alertstore.GroupKey(a.Labels, n.groups)
		groups[k] = append(groups[k], a)
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var res TickResult
	for _, k := range keys {
		n.tickGroup(now, k, groups[k], firing, send, &res)
	}
	return res, nil
}

func (n *Notifier) tickGroup(now int64, key string, members []*alertstore.Alert,
	firing map[string]*alertstore.Alert, send SendFunc, res *TickResult) {
	visible := map[string]bool{}
	var dSeen, dUnseen []string
	var minSince int64
	first := true
	for _, a := range members {
		if a.Firing {
			if n.engine.Visible(now, a.Fingerprint, firing) {
				visible[a.Fingerprint] = true
				if first || a.Since < minSince {
					minSince, first = a.Since, false
				}
			}
			continue
		}
		if st := n.state[key]; st != nil && st.lastSet[a.Fingerprint] {
			dSeen = append(dSeen, a.Fingerprint)
		} else {
			dUnseen = append(dUnseen, a.Fingerprint)
		}
	}
	st := n.state[key]
	do := false
	if st == nil || !st.hasSent {
		do = len(visible) > 0 && now-minSince >= n.wait
	} else {
		nv := false
		for f := range visible {
			if !st.lastSet[f] {
				nv = true
			}
		}
		do = nv || len(dSeen) > 0 ||
			(len(visible) > 0 && now-st.lastSent >= n.repeat)
	}
	if !do {
		for _, f := range dUnseen {
			n.store.Delete(f)
		}
		return
	}
	note := Notification{Group: key, Firing: sortedFPs(visible),
		Resolved: sortedCP(dSeen), At: now}
	if err := send(note); err != nil {
		res.Failed = append(res.Failed, key)
		return
	}
	res.Sent = append(res.Sent, key)
	if st == nil {
		st = &groupState{lastSet: map[string]bool{}}
		n.state[key] = st
	}
	st.hasSent, st.lastSent, st.lastSet = true, now, visible
	for _, f := range dSeen {
		n.store.Delete(f)
	}
	for _, f := range dUnseen {
		n.store.Delete(f)
	}
	if len(members) == len(dSeen)+len(dUnseen) {
		delete(n.state, key)
	}
}

func sortedFPs(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for f := range set {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

func sortedCP(in []string) []string {
	sort.Strings(in)
	return in
}
