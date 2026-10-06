package certselector

import "strings"

func joinLabels(labels []string) string {
	return strings.Join(labels, ".")
}
