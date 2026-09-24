package lines

type Line struct {
	Content string
	EOL     string
}

func Split(data []byte) []Line {
	if len(data) == 0 {
		return nil
	}
	var result []Line
	for start := 0; start < len(data); {
		end := start
		for end < len(data) && data[end] != '\n' {
			end++
		}
		contentEnd := end
		eol := ""
		if end < len(data) {
			eol = "\n"
			if end > start && data[end-1] == '\r' {
				contentEnd--
				eol = "\r\n"
			}
		}
		result = append(result, Line{Content: string(data[start:contentEnd]), EOL: eol})
		if end == len(data) {
			break
		}
		start = end + 1
	}
	return result
}

func Join(lines []Line) []byte {
	var result []byte
	for _, line := range lines {
		result = append(result, line.Content...)
		result = append(result, line.EOL...)
	}
	return result
}
