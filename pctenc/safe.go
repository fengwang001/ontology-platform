package pctenc

// safe[mode][b] reports whether byte b may be emitted unencoded.
//
// Unreserved characters (RFC 3986: A-Z a-z 0-9 - _ . ~) are safe in
// every mode. Each mode adds its own component-specific allow list;
// every other byte is percent-encoded.
var safe = [3][256]bool{}

func init() {
	for c := 'A'; c <= 'Z'; c++ {
		safe[Path][c] = true
		safe[Query][c] = true
		safe[Fragment][c] = true
	}
	for c := 'a'; c <= 'z'; c++ {
		safe[Path][c] = true
		safe[Query][c] = true
		safe[Fragment][c] = true
	}
	for c := '0'; c <= '9'; c++ {
		safe[Path][c] = true
		safe[Query][c] = true
		safe[Fragment][c] = true
	}
	for _, c := range "-_.~" {
		safe[Path][c] = true
		safe[Query][c] = true
		safe[Fragment][c] = true
	}

	// Sub-delims and ':' / '@' allowed inside a path segment, plus '$'.
	for _, c := range ":@&=+$," {
		safe[Path][c] = true
	}

	// A fragment tolerates '/' and '?' in addition to unreserved bytes.
	for _, c := range "/?" {
		safe[Fragment][c] = true
	}
}
