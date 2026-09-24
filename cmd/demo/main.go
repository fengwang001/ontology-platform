package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/hoplex"
	"ontology/netmatch"
	"ontology/trim"
)

var failures int

func check(name string, ok bool) {
	if ok {
		fmt.Printf("[ok]   %s\n", name)
		return
	}
	failures++
	fmt.Printf("[FAIL] %s\n", name)
}

func client(chain, pairs, remote string) (*trim.Result, error) {
	t := trim.New(trim.Config{MaxHops: 16, MaxTrusted: 8})
	if err := t.Trust("10.0.0.0/8"); err != nil {
		return nil, err
	}
	return t.Trim(chain, pairs, remote)
}

func main() {
	// 1. basic: nearest trusted, then one untrusted hop.
	r, err := client("1.2.3.4, 10.0.0.1, 10.0.0.2", "", "10.0.0.2")
	if err != nil {
		fmt.Println("debug1:", err)
	}
	fmt.Printf("debug1: client=%s stop=%d missing=%v\n", r.Client, r.StoppedAt, r.Missing)
	check("1. chain 1.2.3.4,10.0.0.1,10.0.0.2 -> client 1.2.3.4",
		err == nil && r.Client.String() == "1.2.3.4")

	// 2. forged record at the far left must not win.
	r, err = client("9.9.9.9, 1.2.3.4, 10.0.0.1", "", "10.0.0.1")
	check("2. forged 9.9.9.9 prefix does not win -> client 1.2.3.4",
		err == nil && r.Client.String() == "1.2.3.4" && r.StoppedAt == 2)

	// 3. everything trusted -> farthest hop.
	r, err = client("10.1.1.1, 10.2.2.2, 10.0.0.2", "", "10.0.0.2")
	check("3. all trusted -> farthest hop 10.1.1.1",
		err == nil && r.Client.String() == "10.1.1.1" && r.StoppedAt == 3)

	// 4. near hop for=unknown stops trimming at its position.
	r, err = client("", "for=unknown, for=10.0.0.1", "10.0.0.1")
	check("4. for=unknown stops trimming at hop 1 (missing)",
		err == nil && r.Missing && r.StoppedAt == 1)

	// 5. IPv4-mapped IPv6 with port is trusted by an IPv4 prefix.
	a, _ := netmatch.ParseAddress("[::ffff:10.0.0.1]:443")
	p, _ := netmatch.ParsePrefix("10.0.0.0/8")
	check("5. [::ffff:10.0.0.1]:443 contained by 10.0.0.0/8",
		netmatch.Contains(p, a))

	// 6. quoted value keeps its semicolon/comma.
	hops, perr := hoplex.ParsePairs(`for="1.2.3.4";proto=https, for=10.0.0.1`)
	check("6. quoted for not split on ; or , -> 1.2.3.4,10.0.0.1",
		perr == nil && len(hops) == 2 && hops[0].Addr.String() == "1.2.3.4")

	// 7. both headers at once: zip from nearest, and order is reported.
	r, err = client("5.5.5.5, 10.0.0.9", "for=10.0.0.9", "10.0.0.9")
	fmt.Printf("       merge order used: %s\n", r.Order)
	check("7. both headers merged zip-from-nearest -> client 5.5.5.5",
		err == nil && r.Client.String() == "5.5.5.5" &&
			r.Order == "zip-from-nearest: chain then pairs at each level")

	// 8. hop limit exceeded: error, no partial result.
	t := trim.New(trim.Config{MaxHops: 2, MaxTrusted: 8})
	_ = t.Trust("10.0.0.0/8")
	r, err = t.Trim("1.2.3.4, 10.0.0.1, 10.0.0.2", "", "10.0.0.2")
	check("8. hop limit exceeded -> ErrHopLimit, no partial result",
		r == nil && errors.Is(err, trim.ErrHopLimit))

	if failures > 0 {
		os.Exit(1)
	}
	fmt.Println("ALL OK")
}
