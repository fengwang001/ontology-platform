package notify

import "unicode/utf8"

// oracleStore 是按题目规则独立实现的朴素参考：
// 每个 (name,loc,ch) 保留登记顺序的全部版本，选择时线性扫描，
// 不依赖生产实现的有序切片、二分等结构。
type oracleVersion struct {
	eff    int64
	body   string
	retire bool
	seq    int
}

type oracleStore struct {
	dl      string
	nextSeq int
	m       map[key][]*oracleVersion
}

func newOracle(dl string) *oracleStore {
	return &oracleStore{dl: dl, m: make(map[key][]*oracleVersion)}
}

func (o *oracleStore) publish(name, loc, ch, body string, eff int64) *Error {
	if !validName(name) || !validChannel(ch) || !validEff(eff) || len(body) == 0 || len(body) > 1000 || !utf8.ValidString(body) {
		return &Error{Reason: ReasonInvalidParam}
	}
	if !validateLanguage(loc) {
		return &Error{Reason: ReasonInvalidLanguage}
	}
	pb, perr := parseBody(body)
	if perr != nil {
		return perr
	}
	_ = pb
	k := key{name, loc, ch}
	o.nextSeq++
	o.m[k] = append(o.m[k], &oracleVersion{eff: eff, body: body, seq: o.nextSeq})
	return nil
}

func (o *oracleStore) retire(name, loc, ch string, eff int64) *Error {
	if !validName(name) || !validChannel(ch) || !validEff(eff) {
		return &Error{Reason: ReasonInvalidParam}
	}
	if !validateLanguage(loc) {
		return &Error{Reason: ReasonInvalidLanguage}
	}
	k := key{name, loc, ch}
	o.nextSeq++
	o.m[k] = append(o.m[k], &oracleVersion{eff: eff, retire: true, seq: o.nextSeq})
	return nil
}

func (o *oracleStore) selected(k key, at int64) *oracleVersion {
	var best *oracleVersion
	for _, v := range o.m[k] {
		if v.eff > at {
			continue
		}
		if best == nil || v.eff > best.eff || (v.eff == best.eff && v.seq > best.seq) {
			best = v
		}
	}
	return best
}

type oracleOutcome struct {
	text, loc, ck string
	eff           int64
	err           *Error
}

func (o *oracleStore) render(name, loc, ch string, at int64, vars map[string]string) oracleOutcome {
	if !validName(name) || at < 0 || !validConcreteChannel(ch) {
		return oracleOutcome{err: &Error{Reason: ReasonInvalidParam}}
	}
	for v := range vars {
		if !validVariable(v) {
			return oracleOutcome{err: &Error{Reason: ReasonInvalidParam}}
		}
	}
	if !validateLanguage(loc) {
		return oracleOutcome{err: &Error{Reason: ReasonInvalidLanguage}}
	}
	chain := fallbackChain(loc, o.dl)
	var first *oracleOutcome
	saw := false
	for _, lang := range chain {
		for _, ck := range []string{ch, "*"} {
			v := o.selected(key{name, lang, ck}, at)
			if v == nil || v.retire {
				continue
			}
			saw = true
			pb, perr := parseBody(v.body)
			if perr != nil {
				continue
			}
			text, missing := pb.render(vars, ch)
			if missing != nil {
				if first == nil {
					first = &oracleOutcome{err: &Error{Reason: ReasonMissingVars, Missing: append([]string(nil), missing...)}}
				}
				continue
			}
			cps := utf8.RuneCountInString(text)
			if limit := channelLimit(ch); limit >= 0 && cps > limit {
				if first == nil {
					first = &oracleOutcome{err: &Error{Reason: ReasonTooLong, Codepoints: cps}}
				}
				continue
			}
			return oracleOutcome{text: text, loc: lang, ck: ck, eff: v.eff}
		}
	}
	if !saw {
		return oracleOutcome{err: &Error{Reason: ReasonNoTemplate}}
	}
	return *first
}
