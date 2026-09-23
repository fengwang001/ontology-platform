package lines

import "bytes"

type Line struct {
	Text []byte
	NL   []byte
}

func Split(data []byte) []Line {
	if len(data) == 0 {
		return nil
	}
	ls := make([]Line, 0, bytes.Count(data, []byte{'\n'})+1)
	start := 0
	for i, b := range data {
		if b != '\n' {
			continue
		}
		end := i
		nl := []byte{'\n'}
		if end > start && data[end-1] == '\r' {
			end--
			nl = []byte{'\r', '\n'}
		}
		ls = append(ls, Line{Text: data[start:end], NL: nl})
		start = i + 1
	}
	if start < len(data) {
		ls = append(ls, Line{Text: data[start:], NL: nil})
	}
	return ls
}

func Join(ls []Line) []byte {
	n := 0
	for _, line := range ls {
		n += len(line.Text) + len(line.NL)
	}
	out := make([]byte, 0, n)
	for _, line := range ls {
		out = append(out, line.Text...)
		out = append(out, line.NL...)
	}
	return out
}

func Equal(a, b []Line) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i].Text, b[i].Text) || !bytes.Equal(a[i].NL, b[i].NL) {
			return false
		}
	}
	return true
}
