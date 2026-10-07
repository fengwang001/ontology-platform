package traversal

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

type cursorPayload struct {
	TraversalID string `json:"tid"`
	Sequence    int    `json:"seq"`
}

type traversalState struct {
	id            string
	snapshot      Snapshot
	mode          Mode
	hopLimits     []int
	maxHops       int
	engine        *engineState
	nextSeq       int
	complete      bool
	historyChecks int64
}

func encodeCursor(payload cursorPayload, key []byte) (string, error) {
	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	payloadText := base64.RawURLEncoding.EncodeToString(encodedPayload)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(payloadText))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payloadText + "." + signature, nil
}

func decodeCursor(token string, key []byte) (cursorPayload, error) {
	var payload cursorPayload
	dot := indexLastDot(token)
	if dot < 0 {
		return payload, fmt.Errorf("invalid cursor")
	}
	payloadText := token[:dot]
	signatureText := token[dot+1:]
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(payloadText))
	expectedSignature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(signatureText), []byte(expectedSignature)) {
		return payload, fmt.Errorf("invalid cursor signature")
	}
	encodedPayload, err := base64.RawURLEncoding.DecodeString(payloadText)
	if err != nil {
		return payload, err
	}
	if err := json.Unmarshal(encodedPayload, &payload); err != nil {
		return payload, err
	}
	return payload, nil
}

func indexLastDot(value string) int {
	for i := len(value) - 1; i >= 0; i-- {
		if value[i] == '.' {
			return i
		}
	}
	return -1
}

func newTraversalID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}
