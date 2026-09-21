package slashpath

import "strings"

// Join concatenates elems with '/' and returns the Clean of the result.
// Empty elements are ignored. Join() and Join("", "") both return ".".
func Join(elems ...string) string {
	nonEmpty := make([]string, 0, len(elems))
	for _, e := range elems {
		if e != "" {
			nonEmpty = append(nonEmpty, e)
		}
	}
	if len(nonEmpty) == 0 {
		return "."
	}
	return Clean(strings.Join(nonEmpty, "/"))
}
