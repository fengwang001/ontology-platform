package logical

import "unicode/utf8"

type Kind int

const (
	Blank Kind = iota
	Comment
	Data
)

type Fragment struct {
	Text       string
	Physical   int
	Column     int
	ByteOffset int
}

type Line struct {
	Kind      Kind
	Fragments []Fragment
}

type Scanner struct {
	input    string
	pos      int
	physical int
	checks   int
}

func NewScanner(input string) *Scanner {
	return &Scanner{input: input}
}

func (s *Scanner) Scan() []Line {
	var lines []Line
	for s.pos < len(s.input) {
		raw, start, number, eol := s.readLine()
		first, trailing := s.inspect(raw)
		switch {
		case first < 0:
			lines = append(lines, Line{Kind: Blank})
			s.pos += eol
		case raw[first] == '#' || raw[first] == '!':
			lines = append(lines, Line{Kind: Comment, Fragments: []Fragment{{
				Text: raw, Physical: number, Column: 1, ByteOffset: start,
			}}})
			s.pos += eol
		default:
			lines = append(lines, s.scanData(raw, start, number, eol, first, trailing))
		}
	}
	return lines
}

func (s *Scanner) Checks() int {
	return s.checks
}

func (s *Scanner) readLine() (string, int, int, int) {
	start := s.pos
	number := s.physical + 1
	for s.pos < len(s.input) {
		switch s.input[s.pos] {
		case '\n':
			s.physical++
			return s.input[start:s.pos], start, number, 1
		case '\r':
			s.physical++
			length := 1
			if s.pos+1 < len(s.input) && s.input[s.pos+1] == '\n' {
				length = 2
			}
			return s.input[start:s.pos], start, number, length
		}
		s.pos++
	}
	s.physical++
	return s.input[start:], start, number, 0
}

func (s *Scanner) inspect(line string) (int, int) {
	first := -1
	trailing := 0
	for index := 0; index < len(line); index++ {
		char := line[index]
		s.checks++
		if first < 0 && !isSkipSpace(char) {
			first = index
		}
		if char == '\\' {
			trailing++
		} else {
			trailing = 0
		}
	}
	return first, trailing
}

func (s *Scanner) scanData(raw string, start, number, eol, first, trailing int) Line {
	line := Line{Kind: Data}
	end := len(raw)
	if trailing%2 == 1 {
		end--
		s.pos += eol
	} else {
		s.pos += eol
	}
	line.Fragments = append(line.Fragments, Fragment{
		Text: raw[:end], Physical: number, Column: 1, ByteOffset: start,
	})
	for trailing%2 == 1 && s.pos < len(s.input) {
		next, nextStart, nextNumber, nextEOL := s.readLine()
		nextFirst, nextTrailing := s.inspect(next)
		if nextFirst < 0 {
			s.pos += nextEOL
			break
		}
		nextEnd := len(next)
		if nextTrailing%2 == 1 {
			nextEnd--
		}
		line.Fragments = append(line.Fragments, Fragment{
			Text:       next[nextFirst:nextEnd],
			Physical:   nextNumber,
			Column:     utf8.RuneCountInString(next[:nextFirst]) + 1,
			ByteOffset: nextStart + nextFirst,
		})
		trailing = nextTrailing
		s.pos += nextEOL
	}
	return line
}

func isSkipSpace(char byte) bool {
	return char == ' ' || char == '\t' || char == '\f'
}
