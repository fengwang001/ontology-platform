package notify

import "unicode/utf8"

type Result struct {
	Text       string
	Loc        string
	ChannelKey string
	Eff        int64
}

func channelLimit(ch string) int {
	switch ch {
	case "sms":
		return 70
	case "push":
		return 100
	default:
		return -1 // email 不限
	}
}

// Render 按语言外层、渠道内层的候选顺序选择第一个渲染成功的版本。
func (s *Store) Render(name, loc, ch string, at int64, vars map[string]string) (*Result, *Error) {
	if !validName(name) {
		return nil, &Error{Reason: ReasonInvalidParam}
	}
	if at < 0 {
		return nil, &Error{Reason: ReasonInvalidParam}
	}
	if !validConcreteChannel(ch) {
		return nil, &Error{Reason: ReasonInvalidParam}
	}
	for v := range vars {
		if !validVariable(v) {
			return nil, &Error{Reason: ReasonInvalidParam}
		}
	}
	if !validateLanguage(loc) {
		return nil, &Error{Reason: ReasonInvalidLanguage}
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	chain := fallbackChain(loc, s.dl)
	keyLookups := 0

	type candidateFailure struct {
		missing    []string
		codepoints int
	}
	var firstFailure *candidateFailure
	sawCandidate := false

	channels := [2]string{ch, "*"}
	for _, lang := range chain {
		for _, ck := range channels {
			k := key{name, lang, ck}
			vl := s.m[k]
			keyLookups++
			if vl == nil {
				continue
			}
			v, probe := vl.latestAt(at)
			s.probe.note(probeRecord{k: k, n: len(vl.vers), probe: probe})
			if v == nil || v.retire {
				continue
			}
			sawCandidate = true
			text, missing := v.body.render(vars, ch)
			if missing != nil {
				if firstFailure == nil {
					firstFailure = &candidateFailure{missing: missing}
				}
				continue
			}
			cps := utf8.RuneCountInString(text)
			limit := channelLimit(ch)
			if limit >= 0 && cps > limit {
				if firstFailure == nil {
					firstFailure = &candidateFailure{codepoints: cps}
				}
				continue
			}
			s.renderKeyLookups.Store(int64(keyLookups))
			return &Result{Text: text, Loc: lang, ChannelKey: ck, Eff: v.eff}, nil
		}
	}

	s.renderKeyLookups.Store(int64(keyLookups))
	if !sawCandidate {
		return nil, &Error{Reason: ReasonNoTemplate}
	}
	if firstFailure.missing != nil {
		return nil, &Error{Reason: ReasonMissingVars, Missing: firstFailure.missing}
	}
	return nil, &Error{Reason: ReasonTooLong, Codepoints: firstFailure.codepoints}
}
