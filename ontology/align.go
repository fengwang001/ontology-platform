package ontology

func splitLines(content string) []string {
	if content == "" {
		return nil
	}
	var lines []string
	start := 0
	for i := 0; i < len(content); i++ {
		if content[i] == '\n' {
			lines = append(lines, content[start:i+1])
			start = i + 1
		}
	}
	if start < len(content) {
		lines = append(lines, content[start:])
	}
	return lines
}

func matchLines(child []string, parent []string) []int {
	matches := make([]int, len(child))
	for i := range matches {
		matches[i] = -1
	}
	if len(child) == 0 || len(parent) == 0 {
		return matches
	}

	dp := make([][]int, len(child)+1)
	for i := range dp {
		dp[i] = make([]int, len(parent)+1)
	}
	for i := len(child) - 1; i >= 0; i-- {
		for j := len(parent) - 1; j >= 0; j-- {
			dp[i][j] = dp[i+1][j]
			if dp[i][j+1] > dp[i][j] {
				dp[i][j] = dp[i][j+1]
			}
			if child[i] == parent[j] && dp[i+1][j+1]+1 > dp[i][j] {
				dp[i][j] = dp[i+1][j+1] + 1
			}
		}
	}

	i, j := 0, 0
	for i < len(child) && j < len(parent) {
		if child[i] == parent[j] && dp[i][j] == dp[i+1][j+1]+1 {
			matches[i] = j
			i++
			j++
		} else if dp[i+1][j] >= dp[i][j+1] {
			i++
		} else {
			j++
		}
	}
	return matches
}
