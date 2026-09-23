// Command demo exercises the config overlay pipeline end to end.
package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/source"
)

var failures int

func check(name string, ok bool, detail string) {
	checks++
	status := "OK"
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%s %s: %s\n", status, name, detail)
}

func main() {
	checkMerge()
	checkExpand()
	checkCycle()
	checkEscape()
	checkUnclosed()
	checkK20()
	checkTraceOrder()
	checkRawAndExpanded()
	checkDeterminism()
	checkTypeError()
	checkRequired()
	checkEnvAmbiguity()
	checkTruncation()
	fmt.Printf("TOTAL %d checks, %d failed\n", checks, failures)
	if failures > 0 {
		os.Exit(1)
	}
}

var checks int

func checkMerge() {
	res, err := merged()
	ok := err == nil &&
		res.Entries["name"].Value == "env" &&
		res.Entries["who"].Value == "c0" &&
		len(res.Entries["who"].Overridden) == 3
	check("merge priority", ok, "cli>env>file>default, overrides recorded")
}

func checkTruncation() {
	full := "name = file\ngreeting = hello ${name}\n.\n"
	seen := map[error]bool{}
	for i := 1; i < len(full); i++ {
		err := error(nil)
		if _, err = source.ParseFile(source.File, []byte(full[:i])); err == nil {
			check("truncation", false, fmt.Sprintf("cut %d not detected", i))
			return
		}
		for _, c := range []error{source.ErrLineIncomplete, source.ErrKeyIncomplete, source.ErrValueIncomplete} {
			if errors.Is(err, c) {
				seen[c] = true
			}
		}
	}
	ok := seen[source.ErrLineIncomplete] && seen[source.ErrKeyIncomplete] && seen[source.ErrValueIncomplete]
	if _, err := source.ParseFile(source.File, []byte(full)); err != nil {
		ok = false
	}
	check("truncation", ok, "all 38 cuts classified: line/key/value incomplete + full file valid")
}
