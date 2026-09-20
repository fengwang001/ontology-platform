// Command demo checks each documented pctenc semantic and prints one
// OK/FAIL line per item. It exits 0 only when every check passes.
package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/pctenc"
)

func check(name string, ok bool) bool {
	mark := "OK  "
	if !ok {
		mark = "FAIL"
	}
	fmt.Printf("%s %s\n", mark, name)
	return ok
}

func main() {
	all := true
	run := func(name string, ok bool) { all = check(name, ok) && all }

	unreserved := "ABCXYZabcxyz019-_.~"
	ok := true
	for _, m := range []pctenc.Mode{pctenc.Path, pctenc.Query, pctenc.Fragment} {
		ok = ok && pctenc.Encode(unreserved, m) == unreserved
	}
	run("1 unreserved never encoded", ok)

	run("2 mode safe sets / query space %20",
		pctenc.Encode(":@&=+$,", pctenc.Path) == ":@&=+$," &&
			pctenc.Encode("&=+#", pctenc.Query) == "%26%3D%2B%23" &&
			pctenc.Encode("a b", pctenc.Query) == "a%20b" &&
			pctenc.Encode("/?", pctenc.Fragment) == "/?")

	run("3 upper hex, per-byte UTF-8",
		pctenc.Encode("/", pctenc.Path) == "%2F" &&
			pctenc.Encode("中", pctenc.Query) == "%E4%B8%AD")

	lower, e1 := pctenc.Decode("%2f", pctenc.Path)
	plus, e2 := pctenc.Decode("a+b", pctenc.Query)
	run("4 decode hex any case, '+' literal",
		e1 == nil && lower == "/" && e2 == nil && plus == "a+b")

	ok = true
	for _, bad := range []string{"%", "%A", "%GG", "%2G"} {
		got, err := pctenc.Decode(bad, pctenc.Path)
		ok = ok && errors.Is(err, pctenc.ErrBadEscape) && got == ""
	}
	run("5 bad escapes -> ErrBadEscape, empty result", ok)

	ok = true
	for _, s := range []string{"", "a b/中", "!#$&'()*+,;=:@?", "%", "🙂"} {
		for _, m := range []pctenc.Mode{pctenc.Path, pctenc.Query, pctenc.Fragment} {
			got, err := pctenc.Decode(pctenc.Encode(s, m), m)
			ok = ok && err == nil && got == s
		}
	}
	run("6 round trip Decode(Encode(s,m),m)==s", ok)

	pass, e3 := pctenc.Decode("a:b@c", pctenc.Path)
	ctrl, e4 := pctenc.Decode("%00%1F", pctenc.Query)
	run("7 safe chars pass through, no content whitelist",
		e3 == nil && pass == "a:b@c" && e4 == nil && ctrl == "\x00\x1f")

	run("8 identity for safe input, deterministic",
		pctenc.Encode("plain-1_2.3~", pctenc.Query) == "plain-1_2.3~" &&
			pctenc.Encode("a b", pctenc.Query) == pctenc.Encode("a b", pctenc.Query))

	if !all {
		os.Exit(1)
	}
}
