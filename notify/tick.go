package notify

import (
	"fmt"
	"sort"

	"ontology/alertstore"
)

// Tick 评估所有分组并按组键字节序调用 send；send 为 nil 属参数非法。
// Tick 被接受即推进时钟，与单个 send 的成败无关。
func (n *Notifier) Tick(now int64, send func(Notification) error) (Result, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	var res Result
	if send == nil {
		return res, fmt.Errorf("%w: nil send", ErrInvalid)
	}
	if err := n.admit(now); err != nil {
		return res, err
	}
	n.clock = now
	byGroup := make(map[string][]*alertstore.Alert)
	var firing []*alertstore.Alert
	for _, a := range n.store.Snapshot() {
		key := n.groupKey(a.Labels)
		byGroup[key] = append(byGroup[key], a)
		if a.Firing {
			firing = append(firing, a)
		}
	}
	keys := make([]string, 0, len(byGroup))
	for k := range byGroup {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		n.tickGroup(k, byGroup[k], firing, now, send, &res)
	}
	return res, nil
}

// tickGroup 处理单个分组：计算可见集 V 与待报告 resolved 集 D，
// 按条件 (a)/(b)/(c) 决定是否发送，成功则推进组状态并吸收 resolved。
func (n *Notifier) tickGroup(key string, alerts, firing []*alertstore.Alert, now int64,
	send func(Notification) error, res *Result) {
	st := n.states[key]
	var visible, resolved, dropped []string
	var minSince int64
	for _, a := range alerts {
		if a.Firing {
			if n.sil.Silenced(a.Labels, now) || n.inh.Suppressed(a, firing) {
				continue
			}
			visible = append(visible, a.Fp)
			if len(visible) == 1 || a.Since < minSince {
				minSince = a.Since
			}
		} else if st != nil && st.lastSet[a.Fp] {
			resolved = append(resolved, a.Fp)
		} else {
			dropped = append(dropped, a.Fp)
		}
	}
	sort.Strings(visible)
	sort.Strings(resolved)
	sendIt := false
	if st == nil || !st.hasSent {
		sendIt = len(visible) > 0 && now-minSince >= n.wait // (a)
	} else {
		for _, fp := range visible {
			if !st.lastSet[fp] {
				sendIt = true // (b) 新指纹
				break
			}
		}
		sendIt = sendIt || len(resolved) > 0 || // (b) 有待报告 resolved
			(len(visible) > 0 && now-st.lastSent >= n.repeat) // (c)
	}
	if sendIt {
		note := Notification{Group: key, Firing: orEmpty(visible), Resolved: orEmpty(resolved), At: now}
		if err := send(note); err != nil {
			res.Failed = append(res.Failed, key)
			return // 整组状态（含 dropped）原样保留，待下次 Tick 重判
		}
		if st == nil {
			st = &groupState{}
			n.states[key] = st
		}
		st.hasSent, st.lastSent = true, now
		st.lastSet = make(map[string]bool, len(visible))
		for _, fp := range visible {
			st.lastSet[fp] = true
		}
		for _, fp := range resolved {
			n.store.Remove(fp)
		}
		res.Sent = append(res.Sent, key)
	}
	for _, fp := range dropped {
		n.store.Remove(fp)
	}
	for _, a := range alerts {
		if n.store.Get(a.Fp) != nil {
			return // 组内仍有告警，保留组状态
		}
	}
	delete(n.states, key)
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
