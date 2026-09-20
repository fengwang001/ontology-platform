package main

import (
	"errors"
	"fmt"

	"ontology/pctenc"
)

func main() {
	fail := false
	const pathSafe = ":@&=+$,"
	check := func(name string, ok bool) {
		status := "OK"
		if !ok {
			status = "FAIL"
			fail = true
		}
		fmt.Printf("%s  %s\n", status, name)
	}

	// 1. Unreserved characters are never encoded.
	ur := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_.~"
	check("1 unreserved never encoded",
		pctenc.Encode(ur, pctenc.Path) == ur &&
			pctenc.Encode(ur, pctenc.Query) == ur &&
			pctenc.Encode(ur, pctenc.Fragment) == ur)

	// 2. Mode-specific safe sets; spaces in query become %20.
	check("2 mode safe sets",
		pctenc.Encode(pathSafe, pctenc.Path) == pathSafe &&
			pctenc.Encode("&=+#", pctenc.Query) == "%26%3D%2B%23" &&
			pctenc.Encode(" ", pctenc.Query) == "%20" &&
			pctenc.Encode("/?", pctenc.Fragment) == "/?")

	// 3. Uppercase hex, byte-wise UTF-8.
	check("3 uppercase hex and UTF-8 bytewise",
		pctenc.Encode("/", pctenc.Path) == "%2F" &&
			pctenc.Encode("中", pctenc.Query) == "%E4%B8%AD")

	// 4. Decode accepts both hex cases; '+' stays a literal plus.
	d4a, e4a := pctenc.Decode("%2f", pctenc.Path)
	d4b, e4b := pctenc.Decode("%2F", pctenc.Path)
	d4c, e4c := pctenc.Decode("a+b", pctenc.Query)
	check("4 case-insensitive decode and literal plus",
		e4a == nil && e4b == nil && d4a == d4b && d4a == "/" &&
			e4c == nil && d4c == "a+b")

	// 5. Bad escapes return ErrBadEscape and an empty string, no prefix.
	badAll := true
	for _, in := range []string{"%", "%A", "%GG", "%2G"} {
		got, err := pctenc.Decode(in, pctenc.Query)
		if !errors.Is(err, pctenc.ErrBadEscape) || got != "" {
			badAll = false
		}
	}
	got5, err5 := pctenc.Decode("prefix%GG", pctenc.Query)
	check("5 bad escapes: ErrBadEscape and empty result",
		badAll && errors.Is(err5, pctenc.ErrBadEscape) && got5 == "")

	// 6. Round trip for diverse inputs in every mode.
	rtOK := true
	for _, s := range []string{"", ur, " :@&=+$,/?#%+", "中文字符α", "\x00\xff\xfe"} {
		for _, m := range []pctenc.Mode{pctenc.Path, pctenc.Query, pctenc.Fragment} {
			dec, err := pctenc.Decode(pctenc.Encode(s, m), m)
			if err != nil || dec != s {
				rtOK = false
			}
		}
	}
	check("6 roundtrip Decode(Encode(s)) == s", rtOK)

	// 7. Unencoded safe bytes pass through on decode.
	d7, err7 := pctenc.Decode("a:b@c&d=e", pctenc.Path)
	check("7 decode passes safe raw bytes", err7 == nil && d7 == "a:b@c&d=e")

	// 8. Deterministic repeated calls; clean inputs returned untouched.
	e8a, e8b := pctenc.Encode(ur, pctenc.Query), pctenc.Encode(ur, pctenc.Query)
	d8a, derr8a := pctenc.Decode(ur, pctenc.Query)
	d8b, derr8b := pctenc.Decode(ur, pctenc.Query)
	check("8 zero-copy clean path and repeatable",
		e8a == e8b && e8a == ur && d8a == d8b && d8a == ur &&
			derr8a == nil && derr8b == nil)

	if fail {
		fmt.Println("RESULT FAIL")
	} else {
		fmt.Println("RESULT ALL OK")
	}
}
