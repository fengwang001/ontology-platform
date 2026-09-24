package main

import (
	"fmt"

	"ontology/policy"
)

func main() {
	access := policy.New(map[string][]string{
		"reader": {"id", "name"},
		"none":   {},
	})
	ok := access.IsVisible("reader", "id") &&
		!access.IsVisible("reader", "secret") &&
		len(access.VisibleColumns("none")) == 0
	fmt.Println(status(ok, "policy visible, hidden, and empty role"))
}

func status(ok bool, message string) string {
	if ok {
		return "OK " + message
	}
	return "FAIL " + message
}
