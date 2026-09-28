package lines

// Line 是一行：Text 不含行尾，EOL 为原始行尾（"\n"、"\r\n" 或 nil）。
type Line struct {
	Text string
	EOL  string
}

func Split(data []byte) []Line { return nil }

func Join(ls []Line) []byte { return nil }
