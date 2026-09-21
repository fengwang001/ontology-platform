// Command demo exercises the iproute routing table and prints one
// OK/FAIL verdict per check. It exits non-zero if any check fails.
package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/internal/iproute"
)

var failed bool

func check(name string, ok bool) {
	verdict := "OK  "
	if !ok {
		verdict = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", verdict, name)
}

func main() {
	t := iproute.New()

	check("add 0.0.0.0/0", t.Add("0.0.0.0/0", "gw") == nil)
	check("add 10.0.0.0/8", t.Add("10.0.0.0/8", "hop-a") == nil)
	check("add 10.1.0.0/16", t.Add("10.1.0.0/16", "hop-b") == nil)
	check("add 192.168.1.1/32", t.Add("192.168.1.1/32", "direct") == nil)

	check("dup add rejected", errors.Is(t.Add("10.0.0.0/8", "x"), iproute.ErrDuplicateRoute))
	check("empty next rejected", errors.Is(t.Add("9.0.0.0/8", ""), iproute.ErrEmptyNext))
	check("host bits rejected", errors.Is(t.Add("192.168.1.1/24", "x"), iproute.ErrHostBitsSet))
	check("leading zero IP rejected", errors.Is(t.Add("010.0.0.0/8", "x"), iproute.ErrInvalidIP))
	check("leading zero mask rejected", errors.Is(t.Add("10.0.0.0/08", "x"), iproute.ErrInvalidCIDR))
	check("bad mask rejected", errors.Is(t.Add("10.0.0.0/33", "x"), iproute.ErrInvalidCIDR))

	next, matched, err := t.Lookup("10.1.2.3")
	check("lookup longest /16", err == nil && next == "hop-b" && matched == "10.1.0.0/16")

	next, matched, err = t.Lookup("192.168.1.1")
	check("lookup exact /32", err == nil && next == "direct" && matched == "192.168.1.1/32")

	next, matched, err = t.Lookup("203.0.113.7")
	check("lookup default /0", err == nil && next == "gw" && matched == "0.0.0.0/0")

	check("delete missing rejected", errors.Is(t.Delete("172.16.0.0/12"), iproute.ErrRouteNotFound))
	check("delete /16", t.Delete("10.1.0.0/16") == nil)

	next, matched, err = t.Lookup("10.1.2.3")
	check("shorter prefix visible again", err == nil && next == "hop-a" && matched == "10.0.0.0/8")

	check("delete /8", t.Delete("10.0.0.0/8") == nil)
	check("delete /32", t.Delete("192.168.1.1/32") == nil)
	check("delete default", t.Delete("0.0.0.0/0") == nil)

	_, _, err = t.Lookup("10.1.2.3")
	check("no route error", errors.Is(err, iproute.ErrNoRoute))

	if failed {
		os.Exit(1)
	}
}
