package notify

import (
	"sort"
	"sync"
	"unicode/utf8"
)

// ErrorKind 标识可区分的失败原因。
type ErrorKind int

const (
	KindInvalidParam ErrorKind = iota // 参数非法
	KindInvalidLang                   // 语言标签非法
	KindSyntax                        // 模板正文语法错误（仅 Publish）
	KindNoTemplate                    // 无任何候选模板
	KindMissingVars                   // 必需变量缺失
	KindTooLong                       // 渲染结果超出渠道长度
)

// Error 携带失败类别及复现所需的细节。
type Error struct {
	Kind    ErrorKind
	Offset  int      // KindSyntax 时为首个语法错误的字节偏移
	Missing []string // KindMissingVars 时为缺失变量名（升序）
	Length  int      // KindTooLong 时为渲染文本码点数
	detail  string
}

func (e *Error) Error() string { return e.detail }

// Store 是通知模板的版本化存储，所有方法可并发调用。
type Store struct {
	mu       sync.RWMutex
	dl       string
	versions map[tplKey][]version
}

// New 以默认语言 dl 创建存储。
func New(dl string) (*Store, error) {
	if !validLang(dl) {
		return nil, &Error{Kind: KindInvalidLang, detail: "invalid default language"}
	}
	return &Store{dl: dl, versions: make(map[tplKey][]version)}, nil
}

// Publish 登记一个正文版本。
func (s *Store) Publish(name, loc, ch, body string, eff int64) error {
	if !validName(name) || !validChannelKey(ch) || eff < 0 || eff > maxEff || !validBody(body) {
		return &Error{Kind: KindInvalidParam, detail: "invalid publish parameter"}
	}
	if !validLang(loc) {
		return &Error{Kind: KindInvalidLang, detail: "invalid language"}
	}
	segs, perr := parseBody(body)
	if perr != nil {
		return perr
	}

	key := tplKey{name: name, loc: loc, ch: ch}
	s.mu.Lock()
	defer s.mu.Unlock()
	vs := s.versions[key]
	vs = upsertVersion(vs, version{eff: eff, tomb: false, segs: segs})
	s.versions[key] = vs
	return nil
}

// Retire 登记一个墓碑版本。
func (s *Store) Retire(name, loc, ch string, eff int64) error {
	if !validName(name) || !validChannelKey(ch) || eff < 0 || eff > maxEff {
		return &Error{Kind: KindInvalidParam, detail: "invalid retire parameter"}
	}
	if !validLang(loc) {
		return &Error{Kind: KindInvalidLang, detail: "invalid language"}
	}

	key := tplKey{name: name, loc: loc, ch: ch}
	s.mu.Lock()
	defer s.mu.Unlock()
	vs := s.versions[key]
	vs = upsertVersion(vs, version{eff: eff, tomb: true})
	s.versions[key] = vs
	return nil
}

// Result 是一次成功渲染的可复现结果。
type Result struct {
	Text string
	Loc  string
	Ch   string
	Eff  int64

	// 非导出计数器：复现本次 Render 的判定依据。
	maxProbe int // 单次键查找的最大二分探测次数
	lookups  int // 实际执行的键查找次数
}

// Render 按语言回退链、渠道回退与生效时刻渲染模板。
func (s *Store) Render(name, loc, ch string, at int64, vars map[string]string) (*Result, error) {
	if !validName(name) || at < 0 || !concreteChannels[ch] {
		return nil, &Error{Kind: KindInvalidParam, detail: "invalid render parameter"}
	}
	for k := range vars {
		if !validVarName(k) {
			return nil, &Error{Kind: KindInvalidParam, detail: "invalid variable name"}
		}
	}
	if !validLang(loc) {
		return nil, &Error{Kind: KindInvalidLang, detail: "invalid language"}
	}

	s.mu.RLock()
	chain := fallbackChain(loc, s.dl)
	channels := [2]string{ch, "*"}
	type picked struct {
		ver version
		loc string
		ch  string
	}
	var candidates []picked
	maxProbe := 0
	lookups := 0
	for _, l := range chain {
		for _, ck := range channels {
			vs := s.versions[tplKey{name: name, loc: l, ch: ck}]
			lookups++
			v, found, probes := lookupAt(vs, at)
			if probes > maxProbe {
				maxProbe = probes
			}
			if !found || v.tomb {
				continue // 墓碑使该键跳过，但不阻断后续回退
			}
			candidates = append(candidates, picked{ver: v, loc: l, ch: ck})
		}
	}
	s.mu.RUnlock()

	if len(candidates) == 0 {
		return nil, &Error{Kind: KindNoTemplate, detail: "no template candidate"}
	}

	var firstFail *Error
	for idx, cand := range candidates {
		text, fail := renderVersion(cand.ver.segs, vars, ch)
		if fail == nil {
			return &Result{
				Text:     text,
				Loc:      cand.loc,
				Ch:       cand.ch,
				Eff:      cand.ver.eff,
				maxProbe: maxProbe,
				lookups:  lookups,
			}, nil
		}
		if idx == 0 {
			firstFail = fail
		}
	}
	return nil, firstFail
}

type tplKey struct {
	name string
	loc  string
	ch   string
}

type version struct {
	eff  int64
	tomb bool
	segs []seg
}

// upsertVersion 保持版本按 eff 升序；同 eff 后登记的覆盖先登记的。
func upsertVersion(vs []version, v version) []version {
	i := sort.Search(len(vs), func(i int) bool { return vs[i].eff >= v.eff })
	if i < len(vs) && vs[i].eff == v.eff {
		vs[i] = v
		return vs
	}
	vs = append(vs, version{})
	copy(vs[i+1:], vs[i:])
	vs[i] = v
	return vs
}

// lookupAt 返回 eff 不大于 at 的最大 eff 版本，使用二分并统计探测次数。
func lookupAt(vs []version, at int64) (version, bool, int) {
	lo, hi := 0, len(vs)
	probes := 0
	for lo < hi {
		probes++
		mid := int(uint(lo+hi) >> 1)
		if vs[mid].eff <= at {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == 0 {
		return version{}, false, probes
	}
	return vs[lo-1], true, probes
}

var channelLimit = map[string]int{"email": -1, "sms": 70, "push": 100}

func renderVersion(segs []seg, vars map[string]string, reqCh string) (string, *Error) {
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
		for n := range missing {
			names = append(names, n)
		}
		sort.Strings(names)
		return "", &Error{Kind: KindMissingVars, Missing: names, detail: "missing required variables"}
	}

	var b []byte
	for _, sg := range segs {
		if !sg.isVar {
			b = append(b, sg.lit...)
			continue
		}
		if val, ok := vars[sg.name]; ok {
			if reqCh == "email" {
				b = appendEscaped(b, val)
			} else {
				b = append(b, val...)
			}
		} else {
			b = append(b, sg.def...) // 默认文本不转义
		}
	}
	text := string(b)
	if limit := channelLimit[reqCh]; limit >= 0 && utf8.RuneCountInString(text) > limit {
		return "", &Error{Kind: KindTooLong, Length: utf8.RuneCountInString(text), detail: "rendered text exceeds channel limit"}
	}
	return text, nil
}

func appendEscaped(b []byte, v string) []byte {
	for i := 0; i < len(v); i++ {
		switch v[i] {
		case '&':
			b = append(b, "&amp;"...)
		case '<':
			b = append(b, "&lt;"...)
		case '>':
			b = append(b, "&gt;"...)
		case '"':
			b = append(b, "&quot;"...)
		default:
			b = append(b, v[i])
		}
	}
	return b
}
