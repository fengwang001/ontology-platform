package multipart

import (
	"crypto/rand"
	"encoding/base64"
)

const defaultBoundaryTries = 32

func chooseBoundary(parts []Part, maxTries int) (string, error) {
	if maxTries <= 0 {
		maxTries = defaultBoundaryTries
	}
	for attempt := 0; attempt < maxTries; attempt++ {
		candidate, err := randomBoundary()
		if err != nil {
			return "", err
		}
		if boundarySafe(candidate, parts) {
			return candidate, nil
		}
	}
	return "", ErrUnsafeBoundary
}

func randomBoundary() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
	return "", err
	}
	return "ontology-" + base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func boundarySafe(boundary string, parts []Part) bool {
	marker := "--" + boundary
	if !safeIn(marker, nil) {
		return false
	}
	for _, part := range parts {
		if !safeIn(marker, part.Data) {
			return false
		}
	}
	return true
}

func safeIn(marker string, data []byte) bool {
	if len(data) == 0 {
		return true
	}

	// A marker can also straddle the boundary between a part body and framing.
	window := make([]byte, 0, len(data)+len(marker)-1)
	if len(data) >= len(marker)-1 {
		window = append(window, data[len(data)-(len(marker)-1):]...)
	} else {
		window = append(window, data...)
	}
	window = append(window, marker...)
	if containsBytes(window, marker) {
		return false
	}
	return !containsBytes(data, marker)
}

func containsBytes(data []byte, s string) bool {
	return len(data) >= len(s) && indexBytes(data, s) >= 0
}

func indexBytes(data []byte, s string) int {
	for i := 0; i+len(s) <= len(data); i++ {
		if string(data[i:i+len(s)]) == s {
			return i
		}
	}
	return -1
}
