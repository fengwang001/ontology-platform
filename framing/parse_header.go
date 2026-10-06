package framing

import "strings"

// feedHeaderByte implements the immediate-reject rules that fire before the
// header block is complete. Order within one byte: oversize, NUL, bare LF.
func (p *Parser) feedHeaderByte(events []Event, b byte) []Event {
	// The first MaxHeaderBytes bytes are accepted; the next byte rejects.
	if p.hdrLen == p.cfg.MaxHeaderBytes {
		return p.reject(events, RejectHeaderTooLarge)
	}
	if b == 0 {
		return p.reject(events, RejectZeroByte)
	}
	if b == '\n' && !p.lastWasCR {
		return p.reject(events, RejectBareLF)
	}
	p.hdrBuf = append(p.hdrBuf, b)
	p.hdrLen++
	p.lastWasCR = b == '\r'

	// The block ends at an empty line: the buffer ends with CRLF CRLF.
	n := len(p.hdrBuf)
	if b == '\n' && n >= 4 &&
		p.hdrBuf[n-2] == '\r' &&
		p.hdrBuf[n-3] == '\n' &&
		p.hdrBuf[n-4] == '\r' {
		return p.finishHeaders(events)
	}
	return events
}

type parsedHeader struct {
	name  string
	value string
}

// finishHeaders validates the complete header block in the mandated order and
// either emits HeaderComplete and enters the body phase, or rejects.
func (p *Parser) finishHeaders(events []Event) []Event {
	block := p.hdrBuf[:len(p.hdrBuf)-2] // strip the CRLF of the empty line
	lines := splitCRLF(block)

	method, target, version, syntaxOK := parseRequestLine(string(lines[0]))
	var headers []parsedHeader
	if syntaxOK {
		headers = make([]parsedHeader, 0, len(lines)-1)
		for _, line := range lines[1:] {
			if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
				syntaxOK = false
				break
			}
			colon := bytesIndexByte(line, ':')
			if colon <= 0 {
				syntaxOK = false
				break
			}
			name := line[:colon]
			if bytesHasSpaceOrTab(name) {
				syntaxOK = false
				break
			}
			headers = append(headers, parsedHeader{
				name:  strings.ToLower(string(name)),
				value: strings.TrimSpace(string(line[colon+1:])),
			})
		}
	}
	if !syntaxOK {
		p.hdrBuf = p.hdrBuf[:0]
		return p.reject(events, RejectSyntax)
	}

	var contentLengths []string
	var teValues []string
	for _, h := range headers {
		switch h.name {
		case "content-length":
			contentLengths = append(contentLengths, h.value)
		case "transfer-encoding":
			teValues = append(teValues, h.value)
		}
	}

	hasCL := len(contentLengths) > 0
	hasTE := len(teValues) > 0
	if hasCL && hasTE {
		p.hdrBuf = p.hdrBuf[:0]
		return p.reject(events, RejectLengthAndTransferEncoding)
	}

	var length uint64
	if hasCL {
		v, overflow, ok := parseContentLengths(contentLengths)
		if !ok {
			p.hdrBuf = p.hdrBuf[:0]
			return p.reject(events, RejectContentLengthInvalid)
		}
		if overflow || v > p.cfg.MaxBodyBytes {
			p.hdrBuf = p.hdrBuf[:0]
			return p.reject(events, RejectLengthTooLarge)
		}
		length = v
	}

	if hasTE {
		if len(teValues) != 1 || strings.ToLower(teValues[0]) != "chunked" {
			p.hdrBuf = p.hdrBuf[:0]
			return p.reject(events, RejectTransferEncodingUnsupported)
		}
	}

	p.method = method
	p.target = target
	p.version = version
	p.hdrBuf = p.hdrBuf[:0]

	switch {
	case hasTE:
		p.mode = BodyChunked
	case hasCL:
		p.mode = BodyFixedLength
	default:
		p.mode = BodyNone
	}
	events = append(events, p.headerEvent())
	switch {
	case hasTE:
		p.phase = phaseChunkSize
		p.lineBuf = p.lineBuf[:0]
	case hasCL:
		p.fixedLeft = length
		if length == 0 {
			return p.endRequest(events)
		}
		p.phase = phaseFixed
	default:
		p.mode = BodyNone
		return p.endRequest(events)
	}
	return events
}

func (p *Parser) headerEvent() Event {
	return Event{
		Kind:    EventHeaderComplete,
		Method:  p.method,
		Target:  p.target,
		Version: p.version,
		Mode:    p.mode,
	}
}

// endRequest emits any buffered body bytes and RequestEnd, then resets the
// per-request state so the next byte starts a fresh pipelined request.
func (p *Parser) endRequest(events []Event) []Event {
	events = p.flushBody(events)
	p.phase = phaseHeaders
	p.hdrLen = 0
	p.lastWasCR = false
	p.fixedLeft = 0
	p.bodySeen = 0
	p.chunkLeft = 0
	p.lineBuf = p.lineBuf[:0]
	return append(events, Event{Kind: EventRequestEnd})
}

// splitCRLF splits a CRLF-terminated block without its final CRLF into
// individual lines. Every line boundary is known to be CRLF because bare LFs
// are rejected during streaming.
func splitCRLF(block []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i := 0; i < len(block); i++ {
		if block[i] == '\r' && i+1 < len(block) && block[i+1] == '\n' {
			lines = append(lines, block[start:i])
			i++
			start = i + 1
		}
	}
	// The block ends with the CRLF that precedes the terminating empty line,
	// so there is no trailing line after the last CRLF boundary.
	if start < len(block) {
		lines = append(lines, block[start:])
	}
	return lines
}

// parseRequestLine parses "METHOD SP TARGET SP VERSION". The version is
// accepted either as "HTTP/1.1"/"HTTP/1.0" or the bare "1.1"/"1.0" and is
// normalized to "1.1"/"1.0".
func parseRequestLine(line string) (method, target, version string, ok bool) {
	sp1 := strings.IndexByte(line, ' ')
	if sp1 <= 0 {
		return "", "", "", false
	}
	rest := line[sp1+1:]
	sp2 := strings.IndexByte(rest, ' ')
	if sp2 <= 0 {
		return "", "", "", false
	}
	if strings.IndexByte(rest[sp2+1:], ' ') >= 0 {
		return "", "", "", false
	}
	method = line[:sp1]
	target = rest[:sp2]
	ver := strings.TrimPrefix(rest[sp2+1:], "HTTP/")
	if ver != "1.1" && ver != "1.0" {
		return "", "", "", false
	}
	return method, target, ver, true
}

func hasSpaceOrTab(s string) bool {
	return strings.IndexByte(s, ' ') >= 0 || strings.IndexByte(s, '\t') >= 0
}

func bytesIndexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}

func bytesHasSpaceOrTab(b []byte) bool {
	return bytesIndexByte(b, ' ') >= 0 || bytesIndexByte(b, '\t') >= 0
}

// parseContentLengths validates every Content-Length value: non-empty ASCII
// digits only (the value was already trimmed of surrounding whitespace), and
// all values must be identical. overflow is true when the value is a valid
// digit string that exceeds uint64; such values are reported as "length too
// large", not as an illegal content-length.
func parseContentLengths(values []string) (v uint64, overflow bool, ok bool) {
	first := values[0]
	if first == "" {
		return 0, false, false
	}
	for i := 0; i < len(first); i++ {
		c := first[i]
		if c < '0' || c > '9' {
			return 0, false, false
		}
		d := uint64(c - '0')
		if v > (1<<64-1-d)/10 {
			overflow = true
			continue
		}
		if !overflow {
			v = v*10 + d
		}
	}
	for _, val := range values[1:] {
		if val != first {
			return 0, false, false
		}
	}
	return v, overflow, true
}
