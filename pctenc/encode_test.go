package pctenc

import "testing"

const unreserved = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_.~"

func TestEncodeUnreservedNeverEncoded(t *testing.T) {
	for _, m := range []Mode{Path, Query, Fragment} {
		if got := Encode(unreserved, m); got != unreserved {
			t.Errorf("Encode(unreserved, %v) = %q, want unchanged", m, got)
		}
	}
}

func TestEncodeModeSafeSets(t *testing.T) {
	cases := []struct {
		name string
		in   string
		m    Mode
		want string
	}{
		{"path safe", ":@&=+$,", Path, ":@&=+$,"},
		{"path slash encoded", "/", Path, "%2F"},
		{"query amp", "&", Query, "%26"},
		{"query equals", "=", Query, "%3D"},
		{"query plus", "+", Query, "%2B"},
		{"query hash", "#", Query, "%23"},
		{"query space", " ", Query, "%20"},
		{"fragment slash question", "/?", Fragment, "/?"},
		{"fragment hash encoded", "#", Fragment, "%23"},
		{"fragment plus encoded", "+", Fragment, "%2B"},
		{"mixed query", "a b&c=1", Query, "a%20b%26c%3D1"},
		{"mixed path", "/a:b/c", Path, "%2Fa:b%2Fc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Encode(tc.in, tc.m); got != tc.want {
				t.Errorf("Encode(%q, %v) = %q, want %q", tc.in, tc.m, got, tc.want)
			}
		})
	}
}

func TestEncodeUTF8BytewiseUppercase(t *testing.T) {
	got := Encode("中", Query)
	const want = "%E4%B8%AD"
	if got != want {
		t.Errorf("Encode(\"中\") = %q, want %q", got, want)
	}

	for _, m := range []Mode{Path, Query, Fragment} {
		got := Encode("a中b~", m)
		want := "a%E4%B8%ADb~"
		if got != want {
			t.Errorf("Encode(%q, %v) = %q, want %q", "a中b~", m, got, want)
		}
	}
}

func TestEncodeEmpty(t *testing.T) {
	for _, m := range []Mode{Path, Query, Fragment} {
		if got := Encode("", m); got != "" {
			t.Errorf("Encode(\"\", %v) = %q, want empty", m, got)
		}
	}
}
