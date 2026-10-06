package framing

import (
	"fmt"
	"math/rand"
	"os"
	"strings"
	"testing"
)

// genStream builds a stream of zero or more (possibly malformed) pipelined
// HTTP/1.x requests followed by arbitrary trailing bytes.
func genStream(rng *rand.Rand, cfg Config) []byte {
	var b []byte
	reqs := rng.Intn(4)
	for j := 0; j < reqs; j++ {
		b = append(b, genRequest(rng, cfg)...)
	}
	if rng.Intn(3) == 0 {
		k := rng.Intn(30)
		for i := 0; i < k; i++ {
			b = append(b, byte(rng.Intn(256)))
		}
	}
	return b
}

func genRequest(rng *rand.Rand, cfg Config) []byte {
	methods := []string{"GET", "POST", "X"}
	method := methods[rng.Intn(len(methods))]
	target := []string{"/", "/x", "/a/b?q=1", "abc"}[rng.Intn(4)]
	ver := []string{"HTTP/1.1", "HTTP/1.0", "1.1", "HTTP/2.0", "1.2"}[rng.Intn(5)]
	line := method + " " + target + " " + ver

	switch rng.Intn(12) {
	case 0:
		line = "POST /x junk HTTP/1.1"
	case 1:
		line = "GET"
	}

	var hdrs []string
	clCount := rng.Intn(3)
	teCount := rng.Intn(3)
	modePick := rng.Intn(4)
	switch {
	case modePick == 0:
		clCount, teCount = 1+rng.Intn(2), 0
	case modePick == 1:
		clCount, teCount = 0, 1+rng.Intn(2)
	case modePick == 2:
		clCount, teCount = 1, 1
	default:
		clCount, teCount = 0, 0
	}
	for k := 0; k < clCount; k++ {
		switch rng.Intn(6) {
		case 0:
			hdrs = append(hdrs, "Content-Length: abc")
		case 1:
			hdrs = append(hdrs, "Content-Length: ")
		case 2:
			hdrs = append(hdrs, "Content-Length: 12")
		default:
			n := rng.Uint64() % (2*cfg.MaxBodyBytes + 1)
			hdrs = append(hdrs, fmt.Sprintf("content-length: %d", n))
		}
	}
	for k := 0; k < teCount; k++ {
		switch rng.Intn(5) {
		case 0:
			hdrs = append(hdrs, "Transfer-Encoding: gzip")
		case 1:
			hdrs = append(hdrs, "Transfer-Encoding: chunked, gzip")
		default:
			hdrs = append(hdrs, "transfer-encoding: chunked")
		}
	}
	if rng.Intn(3) == 0 {
		hdrs = append(hdrs, []string{
			"X-Ok: v",
			"Bad : v",
			": v",
			" folded",
			"X: \x00",
		}[rng.Intn(5)])
	}

	header := line + "\r\n" + strings.Join(hdrs, "\r\n") + "\r\n\r\n"
	// Sometimes truncate the header mid-stream to exercise prefixes.
	if rng.Intn(6) == 0 {
		cut := rng.Intn(len(header) + 1)
		return []byte(header[:cut])
	}
	// Rare raw-mutation injection inside the header bytes.
	hdrBytes := []byte(header)
	if rng.Intn(8) == 0 {
		pos := rng.Intn(len(hdrBytes))
		hdrBytes[pos] = []byte{'\n', 0, '\r', ' '}[rng.Intn(4)]
	}

	body := genBody(rng, cfg, teCount == 1 && clCount == 0)
	return append(hdrBytes, body...)
}

func genBody(rng *rand.Rand, cfg Config, chunked bool) []byte {
	if chunked && rng.Intn(2) == 0 {
		var b []byte
		chunks := rng.Intn(3)
		for k := 0; k < chunks; k++ {
			size := rng.Intn(int(cfg.MaxBodyBytes) + 2)
			if rng.Intn(8) == 0 {
				b = append(b, []byte(fmt.Sprintf("10000000000000000\r\n"))...)
				return b
			}
			if rng.Intn(6) == 0 {
				b = append(b, []byte(fmt.Sprintf("%xz\r\n", size))...)
				return b
			}
			b = append(b, []byte(fmt.Sprintf("%x", size))...)
			if rng.Intn(4) == 0 {
				b = append(b, []byte(";ext=v")...)
			}
			b = append(b, '\r', '\n')
			payload := randBytes(rng, size)
			b = append(b, payload...)
			if rng.Intn(7) == 0 && size > 0 {
				b = append(b, 'x', '\n')
			} else {
				b = append(b, '\r', '\n')
			}
		}
		b = append(b, '0', '\r', '\n')
		switch rng.Intn(4) {
		case 0:
			b = append(b, []byte("Content-Length: 1\r\n\r\n")...)
		case 1:
			b = append(b, []byte("badline\r\n\r\n")...)
		case 2:
			b = append(b, []byte("X-T: 1\r\n\r\n")...)
		default:
			b = append(b, '\r', '\n')
		}
		return b
	}
	n := rng.Intn(int(2*cfg.MaxBodyBytes) + 1)
	return randBytes(rng, n)
}

func randBytes(rng *rand.Rand, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		// Mostly body-safe bytes.
		b[i] = byte(32 + rng.Intn(95))
	}
	return b
}

func feedChunked(cfg Config, data []byte, cuts []int) []Event {
	p := NewParser(cfg)
	var evs []Event
	prev := 0
	buf := make([]Event, 0, 8)
	for _, c := range cuts {
		buf = p.FeedInto(buf[:0], data[prev:c])
		evs = append(evs, buf...)
		prev = c
		if p.Closed() {
			break
		}
	}
	if prev < len(data) && !p.Closed() {
		buf = p.FeedInto(buf[:0], data[prev:])
		evs = append(evs, buf...)
	}
	return evs
}

func randomCuts(rng *rand.Rand, n int) []int {
	if n == 0 {
		return nil
	}
	var cuts []int
	pos := 0
	for pos < n {
		pos += 1 + rng.Intn(4)
		if pos < n {
			cuts = append(cuts, pos)
		}
	}
	cuts = append(cuts, n)
	return cuts
}

func byteCuts(n int) []int {
	cuts := make([]int, 0, n)
	for i := 1; i <= n; i++ {
		cuts = append(cuts, i)
	}
	return cuts
}

func TestDifferential(t *testing.T) {
	iterations := 1200
	if v := os.Getenv("FRAMING_ITERS"); v != "" {
		fmt.Sscanf(v, "%d", &iterations)
	}
	cfg := Config{MaxHeaderBytes: 64, MaxBodyBytes: 8}
	rng := rand.New(rand.NewSource(20261006))

	var log []string
	for it := 0; it < iterations; it++ {
		data := genStream(rng, cfg)
		want, why := naiveOracle(cfg, data)

		oneShot := runAll(cfg, data)
		perByte := feedChunked(cfg, data, byteCuts(len(data)))
		rng2 := rand.New(rand.NewSource(int64(it) + 1))
		random1 := feedChunked(cfg, data, randomCuts(rng2, len(data)))
		rng3 := rand.New(rand.NewSource(int64(it) + 777))
		random2 := feedChunked(cfg, data, randomCuts(rng3, len(data)))

		all := map[string][]Event{
			"oracle": want, "oneshot": oneShot, "byte": perByte,
			"random1": random1, "random2": random2,
		}
		for name, evs := range all {
			if !eventsEqual(want, evs) {
				t.Fatalf("iter %d %s mismatch\ninput=%q\noracle=%v\n%s=%v\nwhy=%v",
					it, name, data, mergeBodyEvents(want), name, mergeBodyEvents(evs), why)
			}
		}

		if it < 25 {
			log = append(log, fmt.Sprintf(
				"iter %d\n  input : %q\n  why   : %v\n  output: %v",
				it, data, why, mergeBodyEvents(want)))
		}
	}
	log = append([]string{fmt.Sprintf("# differential log: %d iterations, cfg=%+v", iterations, cfg)}, log...)
	if err := os.WriteFile("differential.log", []byte(strings.Join(log, "\n")+"\n"), 0o644); err != nil {
		t.Logf("log write: %v", err)
	}
	t.Logf("differential: %d iterations OK; first %d cases logged to differential.log", iterations, 25)
}
