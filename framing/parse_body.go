package framing

import "strings"

func isHex(b byte) bool {
	switch {
	case b >= '0' && b <= '9', b >= 'a' && b <= 'f', b >= 'A' && b <= 'F':
		return true
	}
	return false
}

func hexVal(b byte) (uint64, bool) {
	switch {
	case b >= '0' && b <= '9':
		return uint64(b - '0'), true
	case b >= 'a' && b <= 'f':
		return uint64(b-'a') + 10, true
	case b >= 'A' && b <= 'F':
		return uint64(b-'A') + 10, true
	}
	return 0, false
}

// parseHexSize parses the accumulated chunk-size token. An empty token or a
// value exceeding 64 bits is a chunk-format error.
func parseHexSize(buf []byte) (uint64, bool) {
	if len(buf) == 0 {
		return 0, false
	}
	var v uint64
	for _, b := range buf {
		d, _ := hexVal(b)
		if v > (1<<64-1-d)>>4 {
			return 0, false
		}
		v = v<<4 | d
	}
	return v, true
}

// reserveChunk rejects with LengthTooLarge when the chunk size would exceed
// the accumulated body ceiling; otherwise records the size and returns true.
func (p *Parser) reserveChunk(events []Event, size uint64) bool {
	if size > p.cfg.MaxBodyBytes || p.bodySeen > p.cfg.MaxBodyBytes-size {
		p.closed = true
		return false
	}
	p.chunkLeft = size
	return true
}

func (p *Parser) feedBodyByte(events []Event, b byte) []Event {
	switch p.phase {
	case phaseFixed:
		p.pendingBody++
		p.bodySeen++
		p.fixedLeft--
		if p.fixedLeft == 0 {
			return p.endRequest(events)
		}
		return events

	case phaseChunkSize:
		switch {
		case isHex(b):
			if len(p.lineBuf) >= 16 {
				return p.reject(events, RejectChunkFormatInvalid)
			}
			p.lineBuf = append(p.lineBuf, b)
			return events
		case b == ';':
			size, ok := parseHexSize(p.lineBuf)
			if !ok || !p.reserveChunk(events, size) {
				return p.chunkError(events, ok)
			}
			p.phase = phaseChunkExt
			return events
		case b == '\r':
			size, ok := parseHexSize(p.lineBuf)
			if !ok || !p.reserveChunk(events, size) {
				return p.chunkError(events, ok)
			}
			p.phase = phaseChunkCR
			return events
		default:
			return p.reject(events, RejectChunkFormatInvalid)
		}

	case phaseChunkExt:
		if b == '\r' {
			p.phase = phaseChunkCR
		} else if b == '\n' || b == 0 {
			return p.reject(events, RejectChunkFormatInvalid)
		}
		return events

	case phaseChunkCR:
		if b != '\n' {
			return p.reject(events, RejectChunkFormatInvalid)
		}
		p.lineBuf = p.lineBuf[:0]
		if p.chunkLeft == 0 {
			p.phase = phaseTrailerLine
		} else {
			p.phase = phaseChunkData
		}
		return events

	case phaseChunkData:
		p.pendingBody++
		p.bodySeen++
		p.chunkLeft--
		if p.chunkLeft == 0 {
			events = p.flushBody(events)
			p.phase = phaseChunkDataCR
		}
		return events

	case phaseChunkDataCR:
		if b != '\r' {
			return p.reject(events, RejectChunkFormatInvalid)
		}
		p.phase = phaseChunkDataLF
		return events

	case phaseChunkDataLF:
		if b != '\n' {
			return p.reject(events, RejectChunkFormatInvalid)
		}
		p.phase = phaseChunkSize
		return events

	case phaseTrailerLine:
		return p.feedTrailerByte(events, b)

	case phaseTrailerCR:
		if b != '\n' {
			return p.reject(events, RejectChunkFormatInvalid)
		}
		p.phase = phaseTrailerLine
		return events

	case phaseTrailerEndCR:
		if b != '\n' {
			return p.reject(events, RejectChunkFormatInvalid)
		}
		return p.endRequest(events)
	}
	return events
}

// chunkError reports the right category after parseHexSize/reserveChunk
// failed: parse failure is chunk-format, ceiling failure is length-too-large.
func (p *Parser) chunkError(events []Event, parsedOK bool) []Event {
	p.closed = true
	if parsedOK {
		return append(events, Event{Kind: EventReject, Reason: RejectLengthTooLarge})
	}
	return append(events, Event{Kind: EventReject, Reason: RejectChunkFormatInvalid})
}

// feedTrailerByte accumulates one trailer line. Trailer syntax is enforced
// exactly like header syntax; a Content-Length or Transfer-Encoding trailer
// is TrailerInvalid. An empty line terminates the request.
func (p *Parser) feedTrailerByte(events []Event, b byte) []Event {
	if b == '\r' {
		if len(p.lineBuf) == 0 {
			p.phase = phaseTrailerEndCR
			return events
		}
		line := string(p.lineBuf)
		colon := strings.IndexByte(line, ':')
		if colon <= 0 || hasSpaceOrTab(line[:colon]) {
			return p.reject(events, RejectChunkFormatInvalid)
		}
		switch strings.ToLower(line[:colon]) {
		case "content-length", "transfer-encoding":
			return p.reject(events, RejectTrailerInvalid)
		}
		p.lineBuf = p.lineBuf[:0]
		p.phase = phaseTrailerCR
		return events
	}
	if b == '\n' || b == 0 {
		return p.reject(events, RejectChunkFormatInvalid)
	}
	// Trailer storage stays bounded by the header size ceiling.
	if len(p.lineBuf) >= p.cfg.MaxHeaderBytes {
		return p.reject(events, RejectChunkFormatInvalid)
	}
	p.lineBuf = append(p.lineBuf, b)
	return events
}
