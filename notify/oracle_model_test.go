package notify_test

import (
	"sort"

	"ontology/alertstore"
	"ontology/notify"
	"ontology/suppress"
)

// oraAlert/oraGroup/oracle 是独立于实现的逐步朴素模型（指纹为共享确定性定义）。
type oraAlert struct {
	fp     string
	labels map[string]string
	since  int64
	firing bool
}
type oraGroup struct {
	hasSent  bool
	lastSent int64
	lastSet  map[string]bool
}
type oracle struct {
	groups    []string
	w, r, max int64
	rules     []suppress.InhibitRule
	silInt    map[string][2]int64
	silM      map[string]map[string]string
	alerts    map[string]*oraAlert
	state     map[string]*oraGroup
	now       int64
	hasTime   bool
	log       []notify.Notification
}

func newOracle(cfg notify.Config) *oracle {
	return &oracle{groups: cfg.GroupLabels, w: cfg.Wait, r: cfg.Repeat, max: int64(cfg.MaxAlerts),
		rules: cfg.Rules, silInt: map[string][2]int64{}, silM: map[string]map[string]string{},
		alerts: map[string]*oraAlert{}, state: map[string]*oraGroup{}}
}

func matchL(m suppress.Matcher, l map[string]string) bool {
	for k, v := range m {
		if l[k] != v {
			return false
		}
	}
	return true
}

func equalL(a, b map[string]string, ns []string) bool {
	for _, n := range ns {
		if a[n] != b[n] {
			return false
		}
	}
	return true
}

func (o *oracle) visible(t int64, fp string, firing map[string]*oraAlert) bool {
	tgt := firing[fp]
	for id, iv := range o.silInt {
		if iv[0] <= t && t < iv[1] && matchL(o.silM[id], tgt.labels) {
			return false
		}
	}
	for _, rule := range o.rules {
		if !matchL(rule.Target, tgt.labels) {
			continue
		}
		for sfp, s := range firing {
			if sfp != fp && matchL(rule.Source, s.labels) && equalL(s.labels, tgt.labels, rule.Equal) {
				return false
			}
		}
	}
	return true
}

func sortedSet(V map[string]bool) []string {
	out := make([]string, 0, len(V))
	for f := range V {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

func (o *oracle) fire(l alertstore.Labels, now int64) {
	f := alertstore.Fingerprint(l)
	if a, ok := o.alerts[f]; ok {
		if !a.firing {
			a.firing, a.since = true, now
		}
	} else if int64(len(o.alerts)) < o.max {
		cp := alertstore.Labels{}
		for k, v := range l {
			cp[k] = v
		}
		o.alerts[f] = &oraAlert{fp: f, labels: cp, since: now, firing: true}
	}
	o.now, o.hasTime = now, true
}

func delAll(o *oracle, fs []string) {
	for _, f := range fs {
		delete(o.alerts, f)
	}
}

// oTick 严格按条件 (a)(b)(c) 与失败保留语义逐组重放。
func oTick(o *oracle, now int64, fail func(int) bool) ([]string, []string) {
	if o.hasTime && now < o.now {
		return nil, nil
	}
	o.now, o.hasTime = now, true
	gm, firing := map[string][]*oraAlert{}, map[string]*oraAlert{}
	for _, a := range o.alerts {
		if a.firing {
			firing[a.fp] = a
		}
		k := alertstore.GroupKey(a.labels, o.groups)
		gm[k] = append(gm[k], a)
	}
	keys := make([]string, 0, len(gm))
	for k := range gm {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sent, failed []string
	for _, key := range keys {
		V := map[string]bool{}
		var D, X []string
		minS, first := int64(0), true
		for _, a := range gm[key] {
			st := o.state[key]
			switch {
			case a.firing && o.visible(now, a.fp, firing):
				V[a.fp] = true
				if first || a.since < minS {
					minS, first = a.since, false
				}
			case !a.firing && st != nil && st.lastSet[a.fp]:
				D = append(D, a.fp)
			case !a.firing:
				X = append(X, a.fp)
			}
		}
		st := o.state[key]
		do := false
		if st == nil || !st.hasSent {
			do = len(V) > 0 && now-minS >= o.w
		} else {
			nv := false
			for f := range V {
				nv = nv || !st.lastSet[f]
			}
			do = nv || len(D) > 0 || (len(V) > 0 && now-st.lastSent >= o.r)
		}
		if !do {
			delAll(o, X)
			continue
		}
		rs := append([]string{}, D...)
		sort.Strings(rs)
		if fail(len(o.log)) {
			failed = append(failed, key)
			continue
		}
		o.log = append(o.log, notify.Notification{Group: key, Firing: sortedSet(V), Resolved: rs, At: now})
		sent = append(sent, key)
		if st == nil {
			st = &oraGroup{lastSet: map[string]bool{}}
			o.state[key] = st
		}
		st.hasSent, st.lastSent, st.lastSet = true, now, V
		delAll(o, D)
		delAll(o, X)
		if len(gm[key]) == len(D)+len(X) {
			delete(o.state, key)
		}
	}
	return sent, failed
}
