package token

import "errors"

var ErrInvalidMessage = errors.New("invalid message")

// Tokens is the masked token sequence parsed from one log message.
type Tokens []string

// Parse validates a message and masks words that contain an ASCII digit.
func Parse(msg string) (Tokens, error) {
	if msg == "" || msg[0] == ' ' || msg[len(msg)-1] == ' ' {
		return nil, ErrInvalidMessage
	}

	start := 0
	count := 0
	for i := 0; i <= len(msg); i++ {
		if i < len(msg) && msg[i] != ' ' {
			continue
		}
		if i == len(msg) || i > start {
			word := msg[start:i]
			if len(word) > 64 {
				return nil, ErrInvalidMessage
			}
			count++
			if count > 32 {
				return nil, ErrInvalidMessage
			}
			if i < len(msg) {
				start = i + 1
			}
		} else {
			return nil, ErrInvalidMessage
		}
	}

	words := make(Tokens, 0, count)
	start = 0
	for i := 0; i <= len(msg); i++ {
		if i < len(msg) && msg[i] != ' ' {
			continue
		}
		word := msg[start:i]
		if hasDigit(word) {
			word = "<*>"
		}
		words = append(words, word)
		if i < len(msg) {
			start = i + 1
		}
	}
	return words, nil
}

func hasDigit(word string) bool {
	for i := 0; i < len(word); i++ {
		if word[i] >= '0' && word[i] <= '9' {
			return true
		}
	}
	return false
}
