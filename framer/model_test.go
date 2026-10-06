package framer

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

type modelState uint8

const (
	modelHeaders modelState = iota
	modelFixed
	modelChunkSize
	modelChunkSizeCR
	modelChunkData
	modelChunkDataCR
	modelTrailer
)

type naiveModel struct {
	limits      Limits
	state       modelState
	closed      bool
	headerBytes []byte
	headerLen   uint64
	headerCR    bool
	fixedLeft   uint64
	chunk       naiveChunk
	trailerLine []byte
	trailerCR   bool
}

type naiveChunk struct {
	size      uint64
	remaining uint64
	total     uint64
	digits    int
	overflow  bool
}

const modelMaxUint64 = ^uint64(0)

func runNaiveModel(input []byte, limits Limits) []Event {
	m := naiveModel{limits: limits}
	var events []Event
	for _, b := range input {
		if m.closed {
			break
		}
		switch m.state {
		case modelHeaders:
			events = m.pushHeader(b, events)
		case modelFixed:
			m.fixedLeft--
			events = append(events, Event{Kind: EventBody, BodyLen: 1})
			if m.fixedLeft == 0 {
				events = append(events, Event{Kind: EventEnd})
				m.beginRequest()
			}
		case modelChunkSize:
			events = m.pushChunkSize(b, events)
		case modelChunkSizeCR:
			if b != '\n' {
				events = m.reject(events, ErrorChunkFormat)
				continue
			}
			if m.chunk.size == 0 {
				m.state = modelTrailer
			} else {
				m.state = modelChunkData
			}
		case modelChunkData:
			if m.chunk.remaining == 0 {
				if b != '\r' {
					events = m.reject(events, ErrorChunkFormat)
					continue
				}
				m.state = modelChunkDataCR
				continue
			}
			m.chunk.remaining--
			events = append(events, Event{Kind: EventBody, BodyLen: 1})
		case modelChunkDataCR:
			if b != '\n' {
				events = m.reject(events, ErrorChunkFormat)
				continue
			}
			m.state = modelChunkSize
			m.chunk.size = 0
			m.chunk.digits = 0
			m.chunk.overflow = false
		case modelTrailer:
			events = m.pushTrailer(b, events)
		}
	}
	return events
}

func (m *naiveModel) reject(events []Event, kind ErrorKind) []Event {
	m.closed = true
	return append(events, Event{Kind: EventReject, Error: kind})
}

func (m *naiveModel) beginRequest() {
	m.state = modelHeaders
	m.headerBytes = m.headerBytes[:0]
	m.headerLen = 0
	m.headerCR = false
	m.fixedLeft = 0
	m.chunk = naiveChunk{}
	m.trailerLine = m.trailerLine[:0]
	m.trailerCR = false
}

func (m *naiveModel) pushHeader(b byte, events []Event) []Event {
	if m.headerLen >= m.limits.MaxHeaderBytes {
		return m.reject(events, ErrorHeaderTooLarge)
	}
	m.headerLen++
	if b == 0 {
		return m.reject(events, ErrorNullByte)
	}
	if b == '\n' {
		if !m.headerCR {
			return m.reject(events, ErrorBareLineFeed)
		}
		m.headerCR = false
		m.headerBytes = append(m.headerBytes, b)
		if len(m.headerBytes) >= 4 && string(m.headerBytes[len(m.headerBytes)-4:]) == "\r\n\r\n" {
			return m.completeHeader(events)
		}
		return events
	}
	m.headerCR = b == '\r'
	m.headerBytes = append(m.headerBytes, b)
	return events
}

func (m *naiveModel) completeHeader(events []Event) []Event {
	lines := strings.Split(strings.TrimSuffix(string(m.headerBytes), "\r\n\r\n"), "\r\n")
	requestLine := strings.Split(lines[0], " ")
	syntaxOK := len(requestLine) == 3 && requestLine[0] != "" && requestLine[1] != "" &&
		(requestLine[2] == "HTTP/1.1" || requestLine[2] == "HTTP/1.0")

	var lengths []string
	var encodings []string
	for _, line := range lines[1:] {
		colon := strings.IndexByte(line, ':')
		if line == "" || modelASCIISpace(line[0]) || colon <= 0 || modelContainsSpace(line[:colon]) {
			syntaxOK = false
		}
		if colon < 0 {
			continue
		}
		name := modelLower(line[:colon])
		value := strings.TrimSpace(line[colon+1:])
		if name == "content-length" {
			lengths = append(lengths, value)
		}
		if name == "transfer-encoding" {
			encodings = append(encodings, value)
		}
	}

	if !syntaxOK {
		return m.reject(events, ErrorSyntax)
	}
	if len(lengths) > 0 && len(encodings) > 0 {
		return m.reject(events, ErrorLengthAndTransferEncoding)
	}
	mode := BodyModeNone
	bodyLength := uint64(0)
	if len(lengths) > 0 {
		if lengths[0] == "" || !modelAllDigits(lengths[0]) {
			return m.reject(events, ErrorInvalidContentLength)
		}
		for _, value := range lengths[1:] {
			if value != lengths[0] {
				return m.reject(events, ErrorInvalidContentLength)
			}
		}
		value, err := strconv.ParseUint(lengths[0], 10, 64)
		if err != nil {
			return m.reject(events, ErrorInvalidContentLength)
		}
		if value > m.limits.MaxBodyBytes {
			return m.reject(events, ErrorBodyTooLarge)
		}
		mode = BodyModeFixed
		bodyLength = value
	}
	if len(encodings) > 0 {
		if len(encodings) != 1 || modelLower(encodings[0]) != "chunked" {
			return m.reject(events, ErrorUnsupportedTransferEncoding)
		}
		mode = BodyModeChunked
	}
	events = append(events, Event{
		Kind:   EventHeaders,
		Mode:   mode,
		Method: requestLine[0],
		Target: requestLine[1],
	})
	if mode == BodyModeFixed {
		if bodyLength == 0 {
			events = append(events, Event{Kind: EventEnd})
			m.beginRequest()
			return events
		}
		m.state = modelFixed
		m.fixedLeft = bodyLength
		return events
	}
	if mode == BodyModeChunked {
		m.state = modelChunkSize
		return events
	}
	events = append(events, Event{Kind: EventEnd})
	m.beginRequest()
	return events
}

func (m *naiveModel) pushChunkSize(b byte, events []Event) []Event {
	if modelHexDigit(b) {
		value := modelHexValue(b)
		if m.chunk.overflow || m.chunk.digits >= 16 || (m.chunk.digits == 15 && m.chunk.size > modelMaxUint64>>4) {
			m.chunk.overflow = true
		} else {
			m.chunk.size = m.chunk.size<<4 | uint64(value)
		}
		m.chunk.digits++
		return events
	}
	if b != '\r' || m.chunk.digits == 0 || m.chunk.overflow {
		return m.reject(events, ErrorChunkFormat)
	}
	if m.chunk.total+m.chunk.size > m.limits.MaxBodyBytes {
		return m.reject(events, ErrorBodyTooLarge)
	}
	m.chunk.total += m.chunk.size
	m.chunk.remaining = m.chunk.size
	m.state = modelChunkSizeCR
	return events
}

func (m *naiveModel) pushTrailer(b byte, events []Event) []Event {
	if uint64(len(m.trailerLine)) >= m.limits.MaxHeaderBytes || b == 0 {
		return m.reject(events, ErrorTrailer)
	}
	if b == '\n' {
		if !m.trailerCR {
			return m.reject(events, ErrorTrailer)
		}
		line := string(m.trailerLine[:len(m.trailerLine)-1])
		m.trailerLine = m.trailerLine[:0]
		m.trailerCR = false
		if line == "" {
			events = append(events, Event{Kind: EventEnd})
			m.beginRequest()
			return events
		}
		colon := strings.IndexByte(line, ':')
		if colon <= 0 || modelASCIISpace(line[0]) || modelContainsSpace(line[:colon]) {
			return m.reject(events, ErrorTrailer)
		}
		name := modelLower(line[:colon])
		if name == "content-length" || name == "transfer-encoding" {
			return m.reject(events, ErrorTrailer)
		}
		return events
	}
	m.trailerCR = b == '\r'
	m.trailerLine = append(m.trailerLine, b)
	return events
}

func modelASCIISpace(b byte) bool {
	return b == ' ' || b == '\t'
}

func modelContainsSpace(value string) bool {
	for i := 0; i < len(value); i++ {
		if modelASCIISpace(value[i]) {
			return true
		}
	}
	return false
}

func modelLower(value string) string {
	buffer := []byte(value)
	for i := range buffer {
		if buffer[i] >= 'A' && buffer[i] <= 'Z' {
			buffer[i] += 'a' - 'A'
		}
	}
	return string(buffer)
}

func modelAllDigits(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}

func modelHexDigit(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

func modelHexValue(b byte) byte {
	switch {
	case b >= '0' && b <= '9':
		return b - '0'
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10
	default:
		return b - 'A' + 10
	}
}

func generateModelCase(rng *rand.Rand) []byte {
	var builder strings.Builder
	requests := 1 + rng.Intn(3)
	for request := 0; request < requests; request++ {
		version := []string{"HTTP/1.1", "HTTP/1.0", "HTTP/2.0", "HTTP/1.1 "}[rng.Intn(4)]
		method := []string{"GET", "POST", "X", "GET "}[rng.Intn(4)]
		target := []string{"/", "/a", "/b?q=1", "bad target"}[rng.Intn(4)]
		builder.WriteString(method + " " + target + " " + version + "\r\n")

		mode := rng.Intn(5)
		switch mode {
		case 0:
		case 1:
			length := rng.Intn(12)
			builder.WriteString("Content-Length: " + strconv.Itoa(length) + "\r\n")
			builder.WriteString("\r\n")
			if length > 0 {
				for i := 0; i < length; i++ {
					builder.WriteByte(byte('a' + i%26))
				}
			}
		case 2:
			builder.WriteString("Transfer-Encoding: chunked\r\n\r\n")
			chunks := rng.Intn(3)
			var total int
			for chunk := 0; chunk < chunks && total < 11; chunk++ {
				size := rng.Intn(4)
				if total+size > 11 {
					size = 0
				}
				builder.WriteString(strconv.FormatInt(int64(size), 16) + "\r\n")
				for i := 0; i < size; i++ {
					builder.WriteByte(byte('a' + (total+i)%26))
				}
				total += size
				builder.WriteString("\r\n")
			}
			if rng.Intn(4) == 0 {
				builder.WriteString("X-Trailer: yes\r\n")
			}
			builder.WriteString("\r\n")
		case 3:
			builder.WriteString("Content-Length: 3\r\nTransfer-Encoding: chunked\r\n\r\n")
		case 4:
			if rng.Intn(2) == 0 {
				builder.WriteString("Content-Length: 3\r\nContent-Length: 3\r\n")
			} else {
				builder.WriteString("Content-Length: 3\r\nContent-Length: 4\r\n")
			}
			builder.WriteString("\r\nabc")
		}
		if mode == 0 {
			builder.WriteString("\r\n")
		}
		if rng.Intn(8) == 0 {
			builder.WriteString("broken"[0 : 1+rng.Intn(6)])
			break
		}
	}
	return []byte(builder.String())
}

func TestRandomEquivalenceAgainstNaiveModel(t *testing.T) {
	if !testing.Verbose() {
		t.Log("rerun with -v to print the required inputs, outputs, and rationale for each case")
	}
	limits := Limits{MaxHeaderBytes: 300, MaxBodyBytes: 11}
	rng := rand.New(rand.NewSource(1589))
	for caseNumber := 0; caseNumber < 1200; caseNumber++ {
		input := generateModelCase(rng)
		reference := mergeBodyEvents(runNaiveModel(input, limits))

		allAtOnce := NewDecoder(limits)
		allEvents := mergeBodyEvents(allAtOnce.Push(input))
		byteAtATime := NewDecoder(limits)
		var byteEvents []Event
		for _, b := range input {
			byteEvents = append(byteEvents, byteAtATime.Push([]byte{b})...)
		}
		byteEvents = mergeBodyEvents(byteEvents)

		randomDecoder := NewDecoder(limits)
		var randomEvents []Event
		for start := 0; start < len(input); {
			end := start + 1 + rng.Intn(7)
			if end > len(input) {
				end = len(input)
			}
			randomEvents = append(randomEvents, randomDecoder.Push(input[start:end])...)
			start = end
		}
		randomEvents = mergeBodyEvents(randomEvents)

		t.Run(fmt.Sprintf("case-%04d", caseNumber), func(t *testing.T) {
			if testing.Verbose() {
				t.Logf("input=%q", input)
				t.Logf("naive=%v all=%v byte=%v random=%v", reference, allEvents, byteEvents, randomEvents)
				t.Log("rationale: independent byte-order model parses headers, then fixed/chunked framing; all feeds use the same byte sequence")
			}
			if fmt.Sprint(allEvents) != fmt.Sprint(reference) {
				t.Fatalf("all-at-once mismatch\ninput=%q\nall=%v\nnaive=%v", input, allEvents, reference)
			}
			if fmt.Sprint(byteEvents) != fmt.Sprint(reference) {
				t.Fatalf("byte-at-a-time mismatch\ninput=%q\nbyte=%v\nnaive=%v", input, byteEvents, reference)
			}
			if fmt.Sprint(randomEvents) != fmt.Sprint(reference) {
				t.Fatalf("random chunks mismatch\ninput=%q\nrandom=%v\nnaive=%v", input, randomEvents, reference)
			}
		})
	}
}
