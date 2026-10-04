package policy

import (
	"errors"
	"testing"
)

func ip(a, b, c, d byte) uint32 {
	return uint32(a)<<24 | uint32(b)<<16 | uint32(c)<<8 | uint32(d)
}

func TestValidatePrefix(t *testing.T) {
	cases := []struct {
		name string
		p    Prefix
		ok   bool
	}{
		{"zero len nonzero addr", Prefix{ip(1, 2, 3, 4), 0}, false},
		{"host bits set", Prefix{ip(10, 1, 0, 1), 16}, false},
		{"valid /16", Prefix{ip(10, 1, 0, 0), 16}, true},
		{"len 32", Prefix{ip(10, 1, 0, 1), 32}, true},
		{"len 33", Prefix{0, 33}, false},
		{"zero", Prefix{0, 0}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePrefix(tc.p)
			if (err == nil) != tc.ok {
				t.Fatalf("got err=%v want ok=%v", err, tc.ok)
			}
			if err != nil && !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("want ErrInvalidArgument, got %v", err)
			}
		})
	}
}

func TestValidateAttrs(t *testing.T) {
	if _, err := ValidateAttrs(Attrs{ASPath: make([]uint32, 65), Origin: 0}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("long aspath: %v", err)
	}
	if _, err := ValidateAttrs(Attrs{Origin: 3}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("bad origin: %v", err)
	}
	if _, err := ValidateAttrs(Attrs{Communities: []uint32{7, 7}}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("dup community: %v", err)
	}
	if _, err := ValidateAttrs(Attrs{Communities: make([]uint32, 33)}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("too many communities: %v", err)
	}
	out, err := ValidateAttrs(Attrs{Communities: []uint32{3, 1, 2}, ASPath: []uint32{9}})
	if err != nil {
		t.Fatal(err)
	}
	if s := out.Communities; s[0] != 1 || s[1] != 2 || s[2] != 3 {
		t.Fatalf("communities not sorted: %v", s)
	}
}

func TestValidateTerms(t *testing.T) {
	bad := []Term{{Match: Match{HasPrefix: true, Prefix: Prefix{ip(10, 0, 0, 0), 8}, GE: 25, LE: 24}}}
	if err := ValidateTerms(bad); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("ge>le: %v", err)
	}
	bad2 := []Term{{Match: Match{HasPrefix: true, Prefix: Prefix{ip(10, 0, 0, 0), 8}, GE: 4, LE: 24}}}
	if err := ValidateTerms(bad2); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("ge<l: %v", err)
	}
	bad3 := []Term{{Action: Action{Kind: 9}}}
	if err := ValidateTerms(bad3); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("bad action: %v", err)
	}
	if err := ValidateTerms(make([]Term, 65)); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("too many terms: %v", err)
	}
}

func TestApplyChain(t *testing.T) {
	localAS, peerAS := uint32(65000), uint32(65001)
	p := Prefix{ip(10, 1, 0, 0), 16}

	// 环路。
	if _, ok := Apply(Attrs{ASPath: []uint32{65000}}, p, []Term{{Action: Action{Kind: ActionAccept}}}, localAS, peerAS, true); ok {
		t.Fatal("loop route must have no post route")
	}

	// 外部邻居 localPref 重置 100；空策略默认拒绝。
	if _, ok := Apply(Attrs{LocalPref: 999, ASPath: []uint32{1}}, p, nil, localAS, peerAS, true); ok {
		t.Fatal("empty policy must default-deny")
	}

	// 内部邻居保留 localPref。
	terms := []Term{{Action: Action{Kind: ActionAccept}}}
	out, ok := Apply(Attrs{LocalPref: 999, ASPath: []uint32{1}}, p, terms, localAS, localAS, false)
	if !ok || out.LocalPref != 999 {
		t.Fatalf("internal keeps localpref: %+v ok=%v", out, ok)
	}
	out, ok = Apply(Attrs{LocalPref: 999, ASPath: []uint32{1}}, p, terms, localAS, peerAS, true)
	if !ok || out.LocalPref != 100 {
		t.Fatalf("external reset localpref: %+v", out)
	}

	// reject 终止。
	rej := []Term{
		{Match: Match{HasCommunity: true, Community: 0x00010029}, Action: Action{Kind: ActionReject}},
		{Action: Action{Kind: ActionAccept}},
	}
	if _, ok := Apply(Attrs{Communities: []uint32{0x00010029}, ASPath: []uint32{1}}, p, rej, localAS, peerAS, true); ok {
		t.Fatal("community reject")
	}

	// next 修改对后续条款可见：置 localPref 后恒 accept。
	nxt := []Term{
		{
			Match: Match{HasPrefix: true, Prefix: Prefix{ip(10, 0, 0, 0), 8}, GE: 8, LE: 24},
			Action: Action{Kind: ActionNext, SetLocalPref: true, LocalPref: 200,
				AddCommunity: true, Community: 42, PrependCount: 2},
		},
		{Action: Action{Kind: ActionAccept}},
	}
	out, ok = Apply(Attrs{ASPath: []uint32{7}}, p, nxt, localAS, peerAS, true)
	if !ok || out.LocalPref != 200 {
		t.Fatalf("next set localpref: %+v", out)
	}
	if len(out.ASPath) != 3 || out.ASPath[0] != peerAS || out.ASPath[1] != peerAS || out.ASPath[2] != 7 {
		t.Fatalf("prepend: %v", out.ASPath)
	}
	if !hasC(out.Communities, 42) {
		t.Fatalf("community add: %v", out.Communities)
	}
	// /25 不命中前缀条件。
	p25 := Prefix{ip(10, 1, 1, 0), 25}
	out, ok = Apply(Attrs{ASPath: []uint32{7}}, p25, nxt, localAS, peerAS, true)
	if !ok || out.LocalPref != 100 {
		t.Fatalf("/25 keeps default pref, got %+v ok=%v", out, ok)
	}

	// 追加已有团体不变；满 32 忽略。
	full := make([]uint32, 32)
	for i := range full {
		full[i] = uint32(i + 1)
	}
	fullTerms := []Term{{Action: Action{Kind: ActionNext, AddCommunity: true, Community: 999}}, {Action: Action{Kind: ActionAccept}}}
	out, ok = Apply(Attrs{Communities: full, ASPath: []uint32{1}}, p, fullTerms, localAS, peerAS, true)
	if !ok || len(out.Communities) != 32 || hasC(out.Communities, 999) {
		t.Fatalf("full community ignored: %v", out.Communities)
	}
	dupTerms := []Term{{Action: Action{Kind: ActionNext, AddCommunity: true, Community: 5}}, {Action: Action{Kind: ActionAccept}}}
	out, ok = Apply(Attrs{Communities: []uint32{5}, ASPath: []uint32{1}}, p, dupTerms, localAS, peerAS, true)
	if !ok || len(out.Communities) != 1 {
		t.Fatalf("existing community unchanged: %v", out.Communities)
	}

	// preprend 超出 64 的部分不插。
	long := make([]uint32, 64)
	pt := []Term{{Action: Action{Kind: ActionNext, PrependCount: 8}}, {Action: Action{Kind: ActionAccept}}}
	out, ok = Apply(Attrs{ASPath: long}, p, pt, localAS, peerAS, true)
	if !ok || len(out.ASPath) != 64 {
		t.Fatalf("prepend cap: %d", len(out.ASPath))
	}
}

func hasC(cs []uint32, c uint32) bool {
	for _, x := range cs {
		if x == c {
			return true
		}
	}
	return false
}

func TestAttrsEqual(t *testing.T) {
	a := Attrs{ASPath: []uint32{1}, Communities: []uint32{2, 3}, Origin: 1, LocalPref: 5, MED: 6}
	b := a.Clone()
	if !AttrsEqual(a, b) {
		t.Fatal("clone must equal")
	}
	b.MED++
	if AttrsEqual(a, b) {
		t.Fatal("med differs")
	}
}
