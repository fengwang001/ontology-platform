package notify

import (
	"sort"
	"unicode/utf8"
)

// naiveRecord 是朴素实现中按登记顺序保存的一条记录。
type naiveRecord struct {
	name string
	loc  string
	ch   string
	eff  int64
	seq  int
	body string
	tomb bool
}

// naiveStore 完全独立于 Store：线性扫描选版本、枚举全部候选。
type naiveStore struct {
	dl      string
	records []naiveRecord
	seq     int
}

func newNaive(dl string) *naiveStore {
	return &naiveStore{dl: dl}
}

func (n *naiveStore) publish(name, loc, ch, body string, eff int64) *Error {
	if !validName(name) || !validChannelKey(ch) || eff < 0 || eff > maxEff || !validBody(body) {
		return &Error{Kind: KindInvalidParam, detail: "naive invalid publish parameter"}
	}
	if !validLang(loc) {
		return &Error{Kind: KindInvalidLang, detail: "naive invalid language"}
	}
	if _, perr := parseBody(body); perr != nil {
		return perr
	}
	n.seq++
	n.records = append(n.records, naiveRecord{name: name, loc: loc, ch: ch, eff: eff, seq: n.seq, body: body})
	return nil
}

func (n *naiveStore) retire(name, loc, ch string, eff int64) *Error {
	if !validName(name) || !validChannelKey(ch) || eff < 0 || eff > maxEff {
		return &Error{Kind: KindInvalidParam, detail: "naive invalid retire parameter"}
	}
	if !validLang(loc) {
		return &Error{Kind: KindInvalidLang, detail: "naive invalid language"}
	}
	n.seq++
	n.records = append(n.records, naiveRecord{name: name, loc: loc, ch: ch, eff: eff, seq: n.seq, tomb: true})
	return nil
}

// activeVersion 线性选出某键下 eff<=at 的最大 eff；同 eff 取 seq 最大者。
func (n *naiveStore) activeVersion(name, loc, ch string, at int64) (naiveRecord, bool) {
	var best naiveRecord
	found := false
	for _, r := range n.records {
		if r.name != name || r.loc != loc || r.ch != ch || r.eff > at {
			continue
		}
		if !found || r.eff > best.eff || (r.eff == best.eff && r.seq > best.seq) {
			best, found = r, true
		}
	}
	return best, found
}

type naiveResult struct {
	text string
	loc  string
	ch   string
	eff  int64
}

func (n *naiveStore) render(name, loc, ch string, at int64, vars map[string]string) (*naiveResult, *Error) {
	if !validName(name) || at < 0 || !concreteChannels[ch] {
		return nil, &Error{Kind: KindInvalidParam, detail: "naive invalid render parameter"}
	}
	for k := range vars {
		if !validVarName(k) {
			return nil, &Error{Kind: KindInvalidParam, detail: "naive invalid variable name"}
		}
	}
	if !validLang(loc) {
		return nil, &Error{Kind: KindInvalidLang, detail: "naive invalid language"}
	}

	type cand struct {
		r   naiveRecord
		loc string
		ch  string
	}
	var cands []cand
	for _, l := range fallbackChain(loc, n.dl) {
		for _, ck := range [2]string{ch, "*"} {
			r, ok := n.activeVersion(name, l, ck, at)
			if !ok || r.tomb {
				continue
			}
			cands = append(cands, cand{r: r, loc: l, ch: ck})
		}
	}
	if len(cands) == 0 {
		return nil, &Error{Kind: KindNoTemplate, detail: "naive no template candidate"}
	}

	var firstFail *Error
	for i, c := range cands {
		segs, perr := parseBody(c.r.body)
		if perr != nil {
			panic("naive: stored body must be valid")
		}
		missing := map[string]struct{}{}
		for _, sg := range segs {
			if sg.isVar && !sg.hasDef {
				if _, ok := vars[sg.name]; !ok {
					missing[sg.name] = struct{}{}
				}
			}
		}
		if len(missing) > 0 {
			names := make([]string, 0, len(missing))
			for nm := range missing {
				names = append(names, nm)
			}
			sort.Strings(names)
			fail := &Error{Kind: KindMissingVars, Missing: names, detail: "naive missing required variables"}
			if i == 0 {
				firstFail = fail
			}
			continue
		}
		var b []byte
		for _, sg := range segs {
			if !sg.isVar {
				b = append(b, sg.lit...)
			} else if val, ok := vars[sg.name]; ok {
				if ch == "email" {
					b = appendEscaped(b, val)
				} else {
					b = append(b, val...)
				}
			} else {
				b = append(b, sg.def...)
			}
		}
		text := string(b)
		limit := channelLimit[ch]
		nr := utf8.RuneCountInString(text)
		if limit >= 0 && nr > limit {
			fail := &Error{Kind: KindTooLong, Length: nr, detail: "naive text too long"}
			if i == 0 {
				firstFail = fail
			}
			continue
		}
		return &naiveResult{text: text, loc: c.loc, ch: c.ch, eff: c.r.eff}, nil
	}
	return nil, firstFail
}
