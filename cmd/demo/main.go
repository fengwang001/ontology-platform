package main

import (
	"fmt"
	"strings"

	"ontology/scan"
)

type splitWant struct {
	text string
	abbr bool
}

func main() {
	sc := scan.NewScanner()
	samples := []string{"HTTPServer", "parseXMLID", "utf8Reader", "sha256Sum", "IDs", "aB"}
	wants := [][]splitWant{
		{{"http", true}, {"server", false}},
		{{"parse", false}, {"xmlid", true}},
		{{"utf", false}, {"8", false}, {"reader", false}},
		{{"sha", false}, {"256", false}, {"sum", false}},
		{{"id", true}, {"s", false}},
		{{"a", false}, {"b", true}},
	}
	ok := true
	for i, sample := range samples {
		tokens, err := sc.Split(sample)
		if err != nil || len(tokens) != len(wants[i]) {
			ok = false
			continue
		}
		for j, got := range tokens {
			if got.Text != wants[i][j].text || got.Acronym != wants[i][j].abbr {
				ok = false
			}
		}
	}
	fmt.Printf("%s six samples split: %s\n", result(ok), strings.Join(samples, ","))
}

func result(ok bool) string {
	if ok {
		return "OK"
	}
	return "FAIL"
}
