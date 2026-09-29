package lines

type Line []byte

func Split(data []byte) []Line {
	var out []Line
	for len(data) > 0 {
		i := indexByte(data, '\n')
		if i < 0 {
			out = append(out, append(Line(nil), data...))
			break
		}
		out = append(out, append(Line(nil), data[:i+1]...))
		data = data[i+1:]
	}
	return out
}

func Join(ls []Line) []byte {
	n := 0
	for _, l := range ls {
		n += len(l)
	}
	out := make([]byte, 0, n)
	for _, l := range ls {
		out = append(out, l...)
	}
	return out
}

func indexByte(data []byte, c byte) int {
	for i, b := range data {
		if b == c {
			return i
		}
	}
	return -1
}
