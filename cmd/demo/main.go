package main

import (
	"fmt"
	"os"
	"strings"

	"ontology/internal/semver"
)

func main() {
	failures := 0
	check := func(name string, ok bool) {
		status := "OK"
		if !ok {
			status = "FAIL"
			failures++
		}
		fmt.Printf("%-4s %s\n", status, name)
	}

	v1, err := semver.Parse("1.2.3-beta.1+build.42")
	check("parse keeps build metadata", err == nil && v1.String() == "1.2.3-beta.1+build.42")

	alpha, _ := semver.Parse("1.0.0-alpha")
	release, _ := semver.Parse("1.0.0")
	check("prerelease ranks below release", semver.Compare(alpha, release) < 0)

	a, _ := semver.Parse("1.0.0-10")
	b, _ := semver.Parse("1.0.0-9")
	check("numeric identifiers compare numerically", semver.Compare(a, b) > 0)

	x, _ := semver.Parse("1.0.0+x")
	y, _ := semver.Parse("1.0.0+y")
	check("build metadata is ignored", semver.Compare(x, y) == 0)

	caret003, _ := semver.ParseRange("^0.0.3")
	v003, _ := semver.Parse("0.0.3")
	v004, _ := semver.Parse("0.0.4")
	check("^0.0.3 matches 0.0.3", caret003.Match(v003))
	check("^0.0.3 rejects 0.0.4", !caret003.Match(v004))

	ge, _ := semver.ParseRange(">=1.0.0")
	pre11, _ := semver.Parse("1.1.0-alpha")
	check(">=1.0.0 hides 1.1.0-alpha", !ge.Match(pre11))

	geGate, _ := semver.ParseRange(">=1.1.0-0")
	check(">=1.1.0-0 admits 1.1.0-alpha", geGate.Match(pre11))

	r1, _ := semver.ParseRange(">=1.2.3")
	r2, _ := semver.ParseRange("<=1.2.3")
	point, err := semver.Intersect(r1, r2)
	v123, _ := semver.Parse("1.2.3")
	v124, _ := semver.Parse("1.2.4")
	check("intersection can be a single point", err == nil &&
		point.Match(v123) && !point.Match(v124))

	r3, _ := semver.ParseRange("<1.2.3")
	r4, _ := semver.ParseRange(">=1.2.3")
	_, err = semver.Intersect(r3, r4)
	check("conflict names both constraints", err != nil &&
		strings.Contains(err.Error(), "<1.2.3") && strings.Contains(err.Error(), ">=1.2.3"))

	r5, _ := semver.ParseRange("<=1.2.3")
	_, err = semver.Intersect(r5, r4)
	check("inclusive bounds are not a conflict", err == nil)

	if failures > 0 {
		fmt.Printf("%d check(s) failed\n", failures)
		os.Exit(1)
	}
}
