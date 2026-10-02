package ontology

import (
	"bytes"
	"strconv"
	"strings"
)

type parsedCaveat struct {
	prefix string
	value  string
	known  bool
	valid  bool
	expiry uint64
}

func parseCaveat(caveat []byte) parsedCaveat {
	colon := bytes.IndexByte(caveat, ':')
	if colon < 0 {
		return parsedCaveat{prefix: string(caveat)}
	}

	prefix := string(caveat[:colon])
	value := caveat[colon+1:]
	parsed := parsedCaveat{prefix: prefix, value: string(value)}

	switch prefix {
	case "exp", "amt":
		limit := uint64(1_000_000_000_000_000)
		if prefix == "amt" {
			limit = 1_000_000_000_000
		}
		number, ok := parseDecimal(value, limit)
		parsed.known = true
		parsed.valid = ok
		parsed.expiry = number
	case "ops":
		parsed.known = true
		parsed.valid = validOps(value)
	case "res":
		parsed.known = true
		parsed.valid = validResourcePrefix(string(value))
	}

	return parsed
}

func caveatSatisfied(parsed parsedCaveat, request Request, now uint64) bool {
	if !parsed.known || !parsed.valid {
		return false
	}

	switch parsed.prefix {
	case "exp":
		return now < parsed.expiry
	case "amt":
		return request.Amount <= parsed.expiry
	case "ops":
		for _, op := range strings.Split(parsed.value, ",") {
			if op == request.Op {
				return true
			}
		}
	case "res":
		if parsed.value == "/" {
			return strings.HasPrefix(request.Res, "/")
		}
		return request.Res == parsed.value || strings.HasPrefix(request.Res, parsed.value+"/")
	}
	return false
}

func validTokenShape(token *Token, maxCaveats, maxCaveatBytes int) bool {
	if token == nil || len(token.ID) < 1 || len(token.ID) > 64 || len(token.Sig) < 1 {
		return false
	}
	if len(token.Caveats) < 0 || len(token.Caveats) > maxCaveats {
		return false
	}
	for _, caveat := range token.Caveats {
		if len(caveat) < 1 || len(caveat) > maxCaveatBytes {
			return false
		}
	}
	return true
}

func validRequest(request Request) bool {
	return request.Op != "" && strings.HasPrefix(request.Res, "/") && request.Amount <= 1_000_000_000_000
}

func parseDecimal(value []byte, limit uint64) (uint64, bool) {
	if len(value) == 0 || len(value) > 16 {
		return 0, false
	}
	if !bytes.Equal(value, []byte("0")) && value[0] == '0' {
		return 0, false
	}
	number, err := strconv.ParseUint(string(value), 10, 64)
	if err != nil || number > limit {
		return 0, false
	}
	return number, true
}

func validOps(value []byte) bool {
	if len(value) == 0 {
		return false
	}
	for _, item := range bytes.Split(value, []byte{','}) {
		if len(item) == 0 {
			return false
		}
		for _, char := range item {
			if !(char >= 'a' && char <= 'z') && !(char >= '0' && char <= '9') && char != '-' {
				return false
			}
		}
	}
	return true
}

func validResourcePrefix(path string) bool {
	if !strings.HasPrefix(path, "/") {
		return false
	}
	if path == "/" {
		return true
	}
	return !strings.HasSuffix(path, "/") && !strings.Contains(path, "//")
}

func cloneBytes(input []byte) []byte {
	if input == nil {
		return nil
	}
	return bytes.Clone(input)
}
