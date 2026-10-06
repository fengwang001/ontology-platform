package certselector

import "strings"

type parsedName struct {
	base     string
	wildcard bool
}

func sanKey(base string, wildcard bool) string {
	if wildcard {
		return "*." + base
	}
	return base
}

func splitLabels(name string) []string {
	return strings.Split(name, ".")
}
