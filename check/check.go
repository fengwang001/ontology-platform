package check

func NaiveFindAll(text, pattern string) []int {
	if pattern == "" || len(pattern) > len(text) {
		return []int{}
	}

	matches := make([]int, 0)
	for start := 0; start <= len(text)-len(pattern); start++ {
		if text[start:start+len(pattern)] == pattern {
			matches = append(matches, start)
		}
	}
	return matches
}
