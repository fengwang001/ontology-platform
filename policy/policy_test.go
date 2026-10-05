package policy

import "testing"

func u32(v uint32) *uint32 { return &v }

func TestValidPrefix(t *testing.T) {
	cases := []struct {
		name string
		p    Prefix
		want bool
	}{
		{"default", Prefix{0, 0}, true},
		{"host route", Prefix{0x0A000001, 32}, true},
		{"aligned /8", Prefix{0x0A000000, 8}, true},
		{"host bits set", Prefix{0x0A000001, 8}, false},
		{"len too large", Prefix{0, 33}, false},
	}
	for _, c := range cases {
		if got := ValidPrefix(c.p); got != c.want {
			t.Errorf("%s: ValidPrefix=%v want %v", c.name, got, c.want)
		}
	}
}

func TestValidAttrs(t *testing.T) {
	longPath := make([]uint32, MaxAsPath+1)
	manyComms := make([]uint32, MaxCommunities+1)
	for i := range manyComms {
		manyComms[i] = uint32(i)
	}
	cases := []struct {
		name string
		a    Attrs
		want bool
	}{
		{"empty", Attrs{}, true},
		{"normal", Attrs{AsPath: []uint32{1, 2}, Origin: 2, Communities: []uint32{5, 6}}, true},
		{"origin too large", Attrs{Origin: 3}, false},
		{"aspath too long", Attrs{AsPath: longPath}, false},
		{"dup community", Attrs{Communities: []uint32{7, 7}}, false},
		{"too many communities", Attrs{Communities: manyComms}, false},
	}
	for _, c := range cases {
		if got := ValidAttrs(c.a); got != c.want {
			t.Errorf("%s: ValidAttrs=%v want %v", c.name, got, c.want)
		}
	}
}

func TestValidTerms(t *testing.T) {
	cases := []struct {
		name  string
		terms []Term
		want  bool
	}{
		{"empty chain", nil, true},
		{"accept all", []Term{{Action: ActionAccept}}, true},
		{"bad action", []Term{{Action: Action(9)}}, false},
		{"ge below len", []Term{{Action: ActionAccept, Match: Match{Prefix: &PrefixCond{Addr: 0x0A000000, Len: 8, Ge: 7, Le: 24}}}}, false},
		{"le above 32", []Term{{Action: ActionAccept, Match: Match{Prefix: &PrefixCond{Addr: 0x0A000000, Len: 8, Ge: 8, Le: 33}}}}, false},
		{"cond host bits", []Term{{Action: ActionAccept, Match: Match{Prefix: &PrefixCond{Addr: 0x0A000001, Len: 8, Ge: 8, Le: 24}}}}, false},
		{"good cond", []Term{{Action: ActionAccept, Match: Match{Prefix: &PrefixCond{Addr: 0x0A000000, Len: 8, Ge: 8, Le: 24}}}}, true},
		{"prepend too large", []Term{{Action: ActionAccept, Mods: Mods{Prepend: 9}}}, false},
		{"prepend negative", []Term{{Action: ActionAccept, Mods: Mods{Prepend: -1}}}, false},
	}
	for _, c := range cases {
		if got := ValidTerms(c.terms); got != c.want {
			t.Errorf("%s: ValidTerms=%v want %v", c.name, got, c.want)
		}
	}
	tooMany := make([]Term, MaxTerms+1)
	if ValidTerms(tooMany) {
		t.Error("over-long term chain should be invalid")
	}
}

func TestEval(t *testing.T) {
	p16 := Prefix{Addr: 0x0A010000, Len: 16} // 10.1.0.0/16
	p25 := Prefix{Addr: 0x0A010180, Len: 25} // 10.1.1.128/25
	cases := []struct {
		name     string
		terms    []Term
		p        Prefix
		attrs    Attrs
		peerAS   uint32
		wantOK   bool
		wantLP   uint32
		wantPath []uint32
		wantComm []uint32
	}{
		{
			name:   "default reject",
			terms:  nil,
			p:      p16,
			attrs:  Attrs{LocalPref: 100},
			peerAS: 65001,
			wantOK: false,
		},
		{
			name: "prefix cond ge le hit",
			terms: []Term{
				{Action: ActionAccept, Match: Match{Prefix: &PrefixCond{Addr: 0x0A000000, Len: 8, Ge: 8, Le: 24}}},
			},
			p:      p16,
			attrs:  Attrs{LocalPref: 100},
			peerAS: 65001,
			wantOK: true,
			wantLP: 100,
		},
		{
			name: "prefix cond le miss",
			terms: []Term{
				{Action: ActionAccept, Match: Match{Prefix: &PrefixCond{Addr: 0x0A000000, Len: 8, Ge: 8, Le: 24}}},
			},
			p:      p25,
			attrs:  Attrs{LocalPref: 100},
			peerAS: 65001,
			wantOK: false,
		},
		{
			name: "community match",
			terms: []Term{
				{Action: ActionReject, Match: Match{Community: u32(0x00010029)}},
				{Action: ActionAccept},
			},
			p:      p16,
			attrs:  Attrs{Communities: []uint32{0x00010029}},
			peerAS: 65001,
			wantOK: false,
		},
		{
			name: "as path match",
			terms: []Term{
				{Action: ActionReject, Match: Match{AS: u32(7)}},
				{Action: ActionAccept},
			},
			p:      p16,
			attrs:  Attrs{AsPath: []uint32{65001, 7}},
			peerAS: 65001,
			wantOK: false,
		},
		{
			name: "next mods visible to later terms",
			terms: []Term{
				{Action: ActionNext, Mods: Mods{LocalPref: u32(200), AddCommunity: u32(42), Prepend: 2}},
				{Action: ActionAccept, Match: Match{Community: u32(42), AS: u32(65001)}},
			},
			p:        p16,
			attrs:    Attrs{AsPath: []uint32{65001}, LocalPref: 100},
			peerAS:   65001,
			wantOK:   true,
			wantLP:   200,
			wantPath: []uint32{65001, 65001, 65001},
			wantComm: []uint32{42},
		},
		{
			name: "add existing community unchanged",
			terms: []Term{
				{Action: ActionAccept, Mods: Mods{AddCommunity: u32(42)}},
			},
			p:        p16,
			attrs:    Attrs{Communities: []uint32{42}},
			peerAS:   65001,
			wantOK:   true,
			wantComm: []uint32{42},
		},
	}
	for _, c := range cases {
		got, ok := Eval(c.terms, c.p, c.attrs, c.peerAS)
		if ok != c.wantOK {
			t.Errorf("%s: accepted=%v want %v", c.name, ok, c.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if got.LocalPref != c.wantLP {
			t.Errorf("%s: localPref=%d want %d", c.name, got.LocalPref, c.wantLP)
		}
		if c.wantPath != nil && !EqualAttrs(got, Attrs{AsPath: c.wantPath, LocalPref: got.LocalPref, Communities: got.Communities}) {
			t.Errorf("%s: asPath=%v want %v", c.name, got.AsPath, c.wantPath)
		}
		if c.wantComm != nil && !EqualAttrs(got, Attrs{AsPath: got.AsPath, LocalPref: got.LocalPref, Communities: c.wantComm}) {
			t.Errorf("%s: communities=%v want %v", c.name, got.Communities, c.wantComm)
		}
	}
}

func TestEvalCaps(t *testing.T) {
	// asPath 前插超出 64 的部分不插。
	full := Attrs{AsPath: make([]uint32, 63)}
	terms := []Term{{Action: ActionAccept, Mods: Mods{Prepend: 8}}}
	got, ok := Eval(terms, Prefix{}, full, 65001)
	if !ok || len(got.AsPath) != MaxAsPath {
		t.Fatalf("prepend cap: len=%d ok=%v", len(got.AsPath), ok)
	}
	// 团体已满 32 个时忽略追加。
	comms := make([]uint32, MaxCommunities)
	for i := range comms {
		comms[i] = uint32(i + 1)
	}
	terms = []Term{{Action: ActionAccept, Mods: Mods{AddCommunity: u32(999)}}}
	got, ok = Eval(terms, Prefix{}, Attrs{Communities: comms}, 65001)
	if !ok || len(got.Communities) != MaxCommunities {
		t.Fatalf("community cap: len=%d ok=%v", len(got.Communities), ok)
	}
}
