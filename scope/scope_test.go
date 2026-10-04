package scope

import "testing"

func TestMatch(t *testing.T) {
	cases := []struct {
		pattern, ref string
		want         bool
	}{
		{"main", "main", true},
		{"main", "maint", false},
		{"release*", "release-2", true},
		{"release*", "release", true},
		{"release*", "releas", false},
		{"*", "anything", true},
		{"*", "", true},
		{"", "x", false},
	}
	for _, c := range cases {
		if got := Match(c.pattern, c.ref); got != c.want {
			t.Errorf("Match(%q,%q)=%v want %v", c.pattern, c.ref, got, c.want)
		}
	}
}

func TestValidPatterns(t *testing.T) {
	cases := []struct {
		in   []string
		want bool
	}{
		{nil, true},
		{[]string{"main", "release*", "*"}, true},
		{[]string{"ma*in"}, false},
		{[]string{"*main"}, false},
		{[]string{""}, false},
		{[]string{"a**"}, false},
	}
	for _, c := range cases {
		if got := ValidPatterns(c.in); got != c.want {
			t.Errorf("ValidPatterns(%q)=%v want %v", c.in, got, c.want)
		}
	}
}

func TestValidName(t *testing.T) {
	ok := []string{"A", "_", "TOKEN", "A_B_1", "ABCDEFGHIJKLMNOPQRSTUVWXYZ_0123456789_ABCDEFGHIJKLMNOPQRSTUV"}
	bad := []string{"", "1TOKEN", "token", "TOK-EN", "TOK EN", "A.B", "TOKÉN"}
	bad = append(bad, "A"+string(make([]byte, 64)))
	for _, n := range ok {
		if len(n) > 64 {
			continue
		}
		if !ValidName(n) {
			t.Errorf("ValidName(%q)=false want true", n)
		}
	}
	for _, n := range bad {
		if ValidName(n) {
			t.Errorf("ValidName(%q)=true want false", n)
		}
	}
	long := "ABCDEFGHIJKLMNOPQRSTUVWXYZABCDEFGHIJKLMNOPQRSTUVWXYZABCDEFGHIJKL" // 64 bytes
	if !ValidName(long) {
		t.Errorf("64-byte name should be valid")
	}
	if ValidName(long + "X") {
		t.Errorf("65-byte name should be invalid")
	}
}

func TestValidValue(t *testing.T) {
	if !ValidValue("x") || !ValidValue(string(make([]byte, 4096))) {
		t.Fatal("boundary valid values rejected")
	}
	if ValidValue("") || ValidValue(string(make([]byte, 4097))) {
		t.Fatal("invalid values accepted")
	}
}

func TestProtected(t *testing.T) {
	pats := []string{"main", "release*"}
	cases := []struct {
		event, ref string
		want       bool
	}{
		{"push", "main", true},
		{"push", "release-2", true},
		{"push", "feat", false},
		{"pr_internal", "main", false},
		{"pr_fork", "main", false},
	}
	for _, c := range cases {
		if got := Protected(c.event, c.ref, pats); got != c.want {
			t.Errorf("Protected(%q,%q)=%v want %v", c.event, c.ref, got, c.want)
		}
	}
}

func TestVisible(t *testing.T) {
	cases := []struct {
		vis     Visibility
		private bool
		repos   []string
		repo    string
		want    bool
	}{
		{All, false, nil, "r1", true},
		{Private, true, nil, "r1", true},
		{Private, false, nil, "r1", false},
		{Selected, false, []string{"r2"}, "r2", true},
		{Selected, true, []string{"r2"}, "r1", false},
		{Visibility(99), false, nil, "r1", false},
	}
	for _, c := range cases {
		if got := Visible(c.vis, c.private, c.repos, c.repo); got != c.want {
			t.Errorf("Visible(vis=%d,priv=%v,%q in %v)=%v want %v", c.vis, c.private, c.repo, c.repos, got, c.want)
		}
	}
}
