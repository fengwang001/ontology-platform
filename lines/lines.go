package lines

type Line struct {
	Body []byte
	EOL  []byte
}

func Split(data []byte) []Line { return nil }

func Join(lines []Line) []byte { return nil }

func Equal(a, b Line) bool { return false }
