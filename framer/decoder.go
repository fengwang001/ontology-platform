package framer

import (
	"strconv"
	"strings"
	"sync"
)

type decoderState uint8

const (
	stateHeaders decoderState = iota
	stateFixedBody
	stateChunkSize
	stateChunkSizeCR
	stateChunkData
	stateChunkCR
	stateTrailer
)

type Decoder struct {
	mu        sync.Mutex
	limits    Limits
	state     decoderState
	closed    bool
	hdr       headerBuffer
	remaining uint64
	chunk     chunkState
	trailer   trailerState
}

func NewDecoder(limits Limits) *Decoder {
	return &Decoder{limits: limits}
}

func (d *Decoder) Push(chunk []byte) []Event {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}

	events := make([]Event, 0)
	for _, b := range chunk {
		if d.closed {
			break
		}
		switch d.state {
		case stateHeaders:
			events = d.pushHeaderByte(b, events)
		case stateFixedBody:
			events = d.pushFixedBodyByte(b, events)
		case stateChunkSize:
			events = d.pushChunkSizeByte(b, events)
		case stateChunkSizeCR:
			if b != '\n' {
				events = d.reject(events, ErrorChunkFormat)
				continue
			}
			if d.chunk.size == 0 {
				d.state = stateTrailer
				d.trailer.reset()
			} else {
				d.state = stateChunkData
			}
		case stateChunkData:
			events = d.pushChunkDataByte(b, events)
		case stateChunkCR:
			if b != '\n' {
				events = d.reject(events, ErrorChunkFormat)
				continue
			}
			d.state = stateChunkSize
			d.resetChunkSize()
		case stateTrailer:
			events = d.pushTrailerByte(b, events)
		}
	}
	return events
}

func (d *Decoder) Closed() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.closed
}

func (d *Decoder) reject(events []Event, kind ErrorKind) []Event {
	d.closed = true
	return append(events, Event{Kind: EventReject, Error: kind})
}

func (d *Decoder) resetForNextRequest() {
	d.state = stateHeaders
	d.remaining = 0
	d.hdr.reset()
	d.chunk.reset()
	d.trailer.reset()
}

type headerBuffer struct {
	bytes     []byte
	pendingCR bool
	length    uint64
}

func (h *headerBuffer) reset() {
	h.bytes = h.bytes[:0]
	h.pendingCR = false
	h.length = 0
}

type chunkState struct {
	size      uint64
	remaining uint64
	total     uint64
	digits    int
	overflow  bool
}

func (c *chunkState) reset() {
	*c = chunkState{}
}

type trailerState struct {
	line      []byte
	pendingCR bool
}

func (t *trailerState) reset() {
	t.line = t.line[:0]
	t.pendingCR = false
}

func (d *Decoder) pushHeaderByte(b byte, events []Event) []Event {
	if d.hdr.length >= d.limits.MaxHeaderBytes {
		return d.reject(events, ErrorHeaderTooLarge)
	}
	d.hdr.length++

	switch b {
	case 0:
		return d.reject(events, ErrorNullByte)
	case '\n':
		if !d.hdr.pendingCR {
			return d.reject(events, ErrorBareLineFeed)
		}
		d.hdr.pendingCR = false
		d.hdr.bytes = append(d.hdr.bytes, b)
		if len(d.hdr.bytes) >= 4 && string(d.hdr.bytes[len(d.hdr.bytes)-4:]) == "\r\n\r\n" {
			return d.completeHeaders(events)
		}
	case '\r':
		d.hdr.pendingCR = true
		d.hdr.bytes = append(d.hdr.bytes, b)
	default:
		d.hdr.pendingCR = false
		d.hdr.bytes = append(d.hdr.bytes, b)
	}
	return events
}

func (d *Decoder) completeHeaders(events []Event) []Event {
	raw := d.hdr.bytes
	request := parseRequest(raw)
	if err := validateRequest(&request, d.limits.MaxBodyBytes); err != "" {
		return d.reject(events, err)
	}

	events = append(events, Event{
		Kind:   EventHeaders,
		Mode:   request.mode,
		Method: request.method,
		Target: request.target,
	})
	d.hdr.bytes = nil
	if request.mode == BodyModeFixed {
		d.state = stateFixedBody
		d.remaining = request.contentLength
		if d.remaining == 0 {
			events = append(events, Event{Kind: EventEnd})
			d.resetForNextRequest()
		}
	} else if request.mode == BodyModeChunked {
		d.state = stateChunkSize
		d.resetChunkSize()
	} else {
		events = append(events, Event{Kind: EventEnd})
		d.resetForNextRequest()
	}
	return events
}

func (d *Decoder) resetChunkSize() {
	d.chunk.size = 0
	d.chunk.remaining = 0
	d.chunk.digits = 0
	d.chunk.overflow = false
}

func (d *Decoder) pushFixedBodyByte(b byte, events []Event) []Event {
	if d.remaining > 0 {
		events = appendBody(events, 1)
		d.remaining--
	}
	if d.remaining == 0 {
		events = append(events, Event{Kind: EventEnd})
		d.resetForNextRequest()
	}
	return events
}

func (d *Decoder) pushChunkSizeByte(b byte, events []Event) []Event {
	switch {
	case isHexDigit(b):
		value := hexValue(b)
		if d.chunk.overflow || d.chunk.digits >= 16 || (d.chunk.digits == 15 && d.chunk.size > maxUint64>>4) {
			d.chunk.overflow = true
		} else {
			d.chunk.size = d.chunk.size<<4 | uint64(value)
		}
		d.chunk.digits++
	case b == '\r':
		if d.chunk.digits == 0 || d.chunk.overflow {
			return d.reject(events, ErrorChunkFormat)
		}
		if d.chunk.total+d.chunk.size < d.chunk.total || d.chunk.total+d.chunk.size > d.limits.MaxBodyBytes {
			return d.reject(events, ErrorBodyTooLarge)
		}
		d.chunk.remaining = d.chunk.size
		d.chunk.total += d.chunk.size
		d.state = stateChunkSizeCR
	case b == '\n':
		return d.reject(events, ErrorChunkFormat)
	default:
		return d.reject(events, ErrorChunkFormat)
	}
	return events
}

func (d *Decoder) pushChunkDataByte(b byte, events []Event) []Event {
	if d.chunk.remaining == 0 {
		if b != '\r' {
			return d.reject(events, ErrorChunkFormat)
		}
		d.state = stateChunkCR
		return events
	}

	d.chunk.remaining--
	events = appendBody(events, 1)
	return events
}

func (d *Decoder) pushTrailerByte(b byte, events []Event) []Event {
	if uint64(len(d.trailer.line)) >= d.limits.MaxHeaderBytes {
		return d.reject(events, ErrorTrailer)
	}
	if b == 0 {
		return d.reject(events, ErrorTrailer)
	}
	if b == '\n' {
		if !d.trailer.pendingCR {
			return d.reject(events, ErrorTrailer)
		}
		line := string(d.trailer.line[:len(d.trailer.line)-1])
		d.trailer.line = d.trailer.line[:0]
		d.trailer.pendingCR = false
		if line == "" {
			events = append(events, Event{Kind: EventEnd})
			d.resetForNextRequest()
			return events
		}
		if !validTrailerLine(line) {
			return d.reject(events, ErrorTrailer)
		}
		return events
	}
	d.trailer.pendingCR = b == '\r'
	d.trailer.line = append(d.trailer.line, b)
	return events
}

func isHexDigit(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

func hexValue(b byte) byte {
	switch {
	case b >= '0' && b <= '9':
		return b - '0'
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10
	default:
		return b - 'A' + 10
	}
}

const maxUint64 = ^uint64(0)

func validTrailerLine(line string) bool {
	colon := strings.IndexByte(line, ':')
	if colon <= 0 || isASCIISpace(line[0]) || containsASCIISpace(line[:colon]) {
		return false
	}
	name := asciiLower(line[:colon])
	return name != "content-length" && name != "transfer-encoding"
}

type requestInfo struct {
	method        string
	target        string
	version       string
	mode          BodyMode
	contentLength uint64
	headers       [][2]string
	syntaxOK      bool
}

func parseRequest(raw []byte) requestInfo {
	text := string(raw)
	lines := strings.Split(strings.TrimSuffix(text, "\r\n\r\n"), "\r\n")
	info := requestInfo{mode: BodyModeNone}
	if len(lines) > 0 {
		parts := strings.Split(lines[0], " ")
		if len(parts) == 3 && parts[0] != "" && parts[1] != "" && isSupportedVersion(parts[2]) {
			info.method = parts[0]
			info.target = parts[1]
			info.version = parts[2]
		}
	}
	info.syntaxOK = info.method != ""
	for _, line := range lines[1:] {
		if line == "" || isASCIISpace(line[0]) {
			info.syntaxOK = false
		}
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			info.syntaxOK = false
			info.headers = append(info.headers, [2]string{line, ""})
			continue
		}
		name := line[:colon]
		if name == "" || containsASCIISpace(name) || (colon > 0 && isASCIISpace(name[len(name)-1])) {
			info.syntaxOK = false
		}
		info.headers = append(info.headers, [2]string{
			name,
			strings.TrimSpace(line[colon+1:]),
		})
	}
	return info
}

func validateRequest(info *requestInfo, maxBody uint64) ErrorKind {
	if !info.syntaxOK {
		return ErrorSyntax
	}

	var lengths []string
	var encodings []string
	for _, header := range info.headers {
		switch asciiLower(header[0]) {
		case "content-length":
			lengths = append(lengths, header[1])
		case "transfer-encoding":
			encodings = append(encodings, header[1])
		}
	}

	if len(lengths) > 0 && len(encodings) > 0 {
		return ErrorLengthAndTransferEncoding
	}
	if err := validateLengths(lengths, info, maxBody); err != "" {
		return err
	}
	if len(encodings) > 0 {
		if len(encodings) != 1 || asciiLower(encodings[0]) != "chunked" {
			return ErrorUnsupportedTransferEncoding
		}
		info.mode = BodyModeChunked
	}
	return ""
}

func validateLengths(lengths []string, info *requestInfo, maxBody uint64) ErrorKind {
	if len(lengths) == 0 {
		return ""
	}
	first := lengths[0]
	if first == "" || !allDigits(first) {
		return ErrorInvalidContentLength
	}
	for _, value := range lengths[1:] {
		if value != first {
			return ErrorInvalidContentLength
		}
	}
	parsed, err := strconv.ParseUint(first, 10, 64)
	if err != nil {
		return ErrorInvalidContentLength
	}
	if parsed > maxBody {
		return ErrorBodyTooLarge
	}
	info.contentLength = parsed
	info.mode = BodyModeFixed
	return ""
}

func isSupportedVersion(version string) bool {
	return version == "HTTP/1.1" || version == "HTTP/1.0"
}

func allDigits(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}

func containsASCIISpace(value string) bool {
	for i := 0; i < len(value); i++ {
		if isASCIISpace(value[i]) {
			return true
		}
	}
	return false
}

func isASCIISpace(b byte) bool {
	return b == ' ' || b == '\t'
}

func asciiLower(value string) string {
	buffer := []byte(value)
	for i := range buffer {
		if buffer[i] >= 'A' && buffer[i] <= 'Z' {
			buffer[i] += 'a' - 'A'
		}
	}
	return string(buffer)
}

func appendBody(events []Event, count uint64) []Event {
	if len(events) > 0 && events[len(events)-1].Kind == EventBody {
		events[len(events)-1].BodyLen += count
		return events
	}
	return append(events, Event{Kind: EventBody, BodyLen: count})
}
