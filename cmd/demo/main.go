// Command demo exercises the iproute table and prints OK/FAIL per check.
package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/internal/iproute"
)

var failed bool

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%-4s %s\n", status, name)
}

func main() {
	tb := iproute.New()

	check("add 10.0.0.0/8", tb.Add("10.0.0.0/8", "hop-a") == nil)
	check("add 10.1.0.0/16", tb.Add("10.1.0.0/16", "hop-b") == nil)
	check("add 0.0.0.0/0", tb.Add("0.0.0.0/0", "gw") == nil)
	check("reject leading-zero ip", errors.Is(tb.Add("192.168.001.0/24", "x"), iproute.ErrInvalidIP))
	check("reject leading-zero mask", errors.Is(tb.Add("10.2.0.0/08", "x"), iproute.ErrInvalidMask))
	check("reject host bits set", errors.Is(tb.Add("192.168.1.1/24", "x"), iproute.ErrHostBitsSet))
	check("reject empty next", errors.Is(tb.Add("10.9.0.0/16", ""), iproute.ErrEmptyNext))
	check("reject duplicate", errors.Is(tb.Add("10.0.0.0/8", "y"), iproute.ErrDuplicateRoute))

	_, m1, e1 := tb.Lookup("10.1.2.3")
	check("longest prefix wins", e1 == nil && m1 == "10.1.0.0/16")

	_, m2, e2 := tb.Lookup("203.0.113.7")
	check("default route catches all", e2 == nil && m2 == "0.0.0.0/0")

	check("delete /16", tb.Delete("10.1.0.0/16") == nil)
	_, m3, e3 := tb.Lookup("10.1.2.3")
	check("shorter prefix visible again", e3 == nil && m3 == "10.0.0.0/8")

	check("delete missing fails", errors.Is(tb.Delete("10.1.0.0/16"), iproute.ErrRouteNotFound))

	tb2 := iproute.New()
	_ = tb2.Add("10.0.0.0/8", "hop-a")
	_, _, err := tb2.Lookup("11.0.0.1")
	check("no route is an error", errors.Is(err, iproute.ErrNoRoute))

	if failed {
		os.Exit(1)
	}
}
