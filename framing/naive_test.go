package framing

import (
	"fmt"
	"strconv"
	"strings"
)

// naiveOracle is an independent, deliberately simple whole-buffer reference
// implementation of the same specification. It is structured as one big
// byte-index loop over an entire connection stream and shares no code with
// the incremental Parser.
func naiveOracle(cfg Config, data []byte) ([]Event, []string) {
	var evs []Event
	var why []string
	n := len(data)
	i := 0

	reject := func(r RejectReason) {
		evs = append(evs, Event{Kind: EventReject, Reason: r})
		why = append(why, fmt.Sprintf("reject@%d %s", i, r))
		n = i // stop consuming any further bytes on this connection
	}

	for i < n {
		// ---- header block ----
		hdrStart := i
		complete := -1
		for i < n {
			if i-hdrStart == cfg.MaxHeaderBytes {
				reject(RejectHeaderTooLarge)
				break
			}
			b := data[i]
			if b == 0 {
				reject(RejectZeroByte)
				break
			}
			if b == '\n' && (i == hdrStart || data[i-1] != '\r') {
				reject(RejectBareLF)
				break
			}
			if b == '\n' && i >= hdrStart+3 &&
				data[i-3] == '\r' && data[i-2] == '\n' && data[i-1] == '\r' {
				complete = i + 1
				i++
				break
			}
			i++
		}
		if complete < 0 {
			break
		}

		block := data[hdrStart : complete-2]
		lines := naiveLines(block)
		method, target, version, okSyntax := naiveRequestLine(lines[0])
		type hdr struct{ name, value string }
		var hs []hdr
		if okSyntax {
			for _, ln := range lines[1:] {
				if strings.HasPrefix(ln, " ") || strings.HasPrefix(ln, "\t") {
					okSyntax = false
					break
				}
				c := strings.IndexByte(ln, ':')
				if c <= 0 || strings.ContainsAny(ln[:c], " \t") {
					okSyntax = false
					break
				}
				hs = append(hs, hdr{
					name:  strings.ToLower(ln[:c]),
					value: strings.TrimSpace(ln[c+1:]),
				})
			}
		}
		if !okSyntax {
			reject(RejectSyntax)
			break
		}
		var cls, tes []string
		for _, h := range hs {
			switch h.name {
			case "content-length":
				cls = append(cls, h.value)
			case "transfer-encoding":
				tes = append(tes, h.value)
			}
		}
		if len(cls) > 0 && len(tes) > 0 {
			reject(RejectLengthAndTransferEncoding)
			break
		}
		var length uint64
		if len(cls) > 0 {
			v, overflow, ok := naiveCL(cls)
			if !ok {
				reject(RejectContentLengthInvalid)
				break
			}
			if overflow || v > cfg.MaxBodyBytes {
				reject(RejectLengthTooLarge)
				break
			}
			length = v
		}
		if len(tes) > 0 && (len(tes) != 1 || strings.ToLower(tes[0]) != "chunked") {
			reject(RejectTransferEncodingUnsupported)
			break
		}

		mode := BodyNone
		switch {
		case len(tes) > 0:
			mode = BodyChunked
		case len(cls) > 0:
			mode = BodyFixedLength
		}
		evs = append(evs, Event{
			Kind: EventHeaderComplete, Method: method, Target: target,
			Version: version, Mode: mode,
		})
		why = append(why, fmt.Sprintf("header@%d mode=%d", hdrStart, mode))

		switch mode {
		case BodyNone:
			evs = append(evs, Event{Kind: EventRequestEnd})
			why = append(why, "end(no-body)")
		case BodyFixedLength:
			if uint64(n-i) < length {
				if rem := uint64(n - i); rem > 0 {
					evs = append(evs, Event{Kind: EventBody, N: int(rem)})
				}
				i = n
				break
			}
			if length > 0 {
				evs = append(evs, Event{Kind: EventBody, N: int(length)})
			}
			i += int(length)
			evs = append(evs, Event{Kind: EventRequestEnd})
			why = append(why, fmt.Sprintf("fixed-body=%d", length))
		case BodyChunked:
			bodyTotal := uint64(0)
			ended := false
		chunkLoop:
			for {
				// Read the chunk-size line with byte-by-byte immediate
				// rejection, mirroring the streaming state machine: only hex
				// digits, then either ';' (extension) or CRLF; LF / NUL /
				// other bytes reject immediately.
				scan := i
				foundCR := -1
				for scan < n {
					c := data[scan]
					if isHexByte(c) {
						scan++
						continue
					}
					if c == ';' {
						scan++
						for scan < n && data[scan] != '\r' && data[scan] != '\n' {
							if data[scan] == 0 {
								i = scan
								reject(RejectChunkFormatInvalid)
								break chunkLoop
							}
							scan++
						}
						if scan < n && data[scan] == '\n' {
							i = scan
							reject(RejectChunkFormatInvalid)
							break chunkLoop
						}
						if scan < n && data[scan] == '\r' {
							foundCR = scan
						}
						break
					}
					if c == '\r' {
						foundCR = scan
						break
					}
					i = scan
					reject(RejectChunkFormatInvalid)
					break chunkLoop
				}
				if foundCR < 0 {
					// Incomplete size line: leave remaining data unparsed.
					i = n
					break
				}
				if foundCR+1 >= n || data[foundCR+1] != '\n' {
					break
				}
				lineEnd := foundCR
				line := string(data[i:lineEnd])
				sizeTok := line
				if s := strings.IndexByte(line, ';'); s >= 0 {
					sizeTok = line[:s]
				}
				size, err := strconv.ParseUint(sizeTok, 16, 64)
				if err != nil || sizeTok == "" {
					i = lineEnd
					reject(RejectChunkFormatInvalid)
					break chunkLoop
				}
				if size > cfg.MaxBodyBytes || bodyTotal > cfg.MaxBodyBytes-size {
					i = lineEnd
					reject(RejectLengthTooLarge)
					break chunkLoop
				}
				i = lineEnd + 2
				if size == 0 {
					// trailers until empty line
					for {
						// Byte-wise immediate checks for the trailer line.
						j := i
						bad := false
						for j < n {
							if data[j] == 0 || data[j] == '\n' {
								i = j
								reject(RejectChunkFormatInvalid)
								bad = true
								break
							}
							if data[j] == '\r' {
								break
							}
							j++
						}
						if bad {
							break chunkLoop
						}
						te := indexCRLF(data, i, n)
						if te < 0 {
							ended = false
							break chunkLoop
						}
						tl := string(data[i:te])
						i = te + 2
						if tl == "" {
							ended = true
							break chunkLoop
						}
						c := strings.IndexByte(tl, ':')
						if c <= 0 || strings.ContainsAny(tl[:c], " \t") {
							i = te
							reject(RejectChunkFormatInvalid)
							break chunkLoop
						}
						nm := strings.ToLower(tl[:c])
						if nm == "content-length" || nm == "transfer-encoding" {
							i = te
							reject(RejectTrailerInvalid)
							break chunkLoop
						}
					}
				}
				if uint64(n-i) < size+2 {
					evs = append(evs, Event{Kind: EventBody, N: int(uint64(n - i))})
					i = n
					break
				}
				evs = append(evs, Event{Kind: EventBody, N: int(size)})
				bodyTotal += size
				if data[i+int(size)] != '\r' || data[i+int(size)+1] != '\n' {
					i = i + int(size)
					reject(RejectChunkFormatInvalid)
					break chunkLoop
				}
				i += int(size) + 2
			}
			if ended {
				evs = append(evs, Event{Kind: EventRequestEnd})
				why = append(why, "end(chunked)")
			}
		}
	}
	return evs, why
}

func indexCRLF(data []byte, i, n int) int {
	for ; i+1 < n; i++ {
		if data[i] == '\r' && data[i+1] == '\n' {
			return i
		}
	}
	return -1
}

func isHexByte(b byte) bool {
	switch {
	case b >= '0' && b <= '9', b >= 'a' && b <= 'f', b >= 'A' && b <= 'F':
		return true
	}
	return false
}

func naiveLines(block []byte) []string {
	var out []string
	start := 0
	for i := 0; i+1 < len(block); i++ {
		if block[i] == '\r' && block[i+1] == '\n' {
			out = append(out, string(block[start:i]))
			i++
			start = i + 1
		}
	}
	if start < len(block) {
		out = append(out, string(block[start:]))
	}
	return out
}

func naiveRequestLine(line string) (string, string, string, bool) {
	f := strings.Split(line, " ")
	if len(f) != 3 || f[0] == "" || f[1] == "" {
		return "", "", "", false
	}
	v := strings.TrimPrefix(f[2], "HTTP/")
	if v != "1.1" && v != "1.0" {
		return "", "", "", false
	}
	return f[0], f[1], v, true
}

func naiveCL(vals []string) (uint64, bool, bool) {
	first := vals[0]
	if first == "" {
		return 0, false, false
	}
	v, err := strconv.ParseUint(first, 10, 64)
	overflow := false
	if err != nil {
		if ne, ok := err.(*strconv.NumError); ok && ne.Err == strconv.ErrRange {
			overflow = true
		} else {
			return 0, false, false
		}
	}
	for _, x := range vals[1:] {
		if x != first {
			return 0, false, false
		}
	}
	return v, overflow, true
}
