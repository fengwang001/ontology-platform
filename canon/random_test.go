package canon_test

import (
	"fmt"
	"math/rand"
	"strings"
)

// randomURL builds random URLs, mixing well-formed and malformed shapes:
// random case, default/non-default/zero-padded/empty ports, trailing
// dots, IPv6 literals, dot segments, empty segments, percent escapes
// (valid, redundant, reserved, broken) and shuffled query items.
func randomURL(rng *rand.Rand) string {
	var b strings.Builder
	scheme := pick(rng, []string{"http", "HTTP", "https", "Https", "ftp"})
	b.WriteString(scheme)
	b.WriteString("://")
	b.WriteString(randomAuthority(rng, scheme))
	b.WriteString(randomPath(rng))
	if rng.Intn(4) > 0 {
		b.WriteByte('?')
		b.WriteString(randomQuery(rng))
	}
	return b.String()
}

func randomAuthority(rng *rand.Rand, scheme string) string {
	if rng.Intn(5) == 0 {
		hosts := []string{"[::1]", "[2001:db8::1]",
			"[2001:0db8:0000:0000:0000:0000:0000:0001]", "[FE80::aB]"}
		return pick(rng, hosts) + randomPort(rng, scheme)
	}
	labels := []string{"Example", "COM", "com.", "a.b", "x-y.z", "LOCALHOST", ""}
	h := pick(rng, labels) + pick(rng, []string{".", ""})
	if rng.Intn(4) == 0 {
		h += "."
	}
	return h + randomPort(rng, scheme)
}

func randomPort(rng *rand.Rand, scheme string) string {
	switch rng.Intn(6) {
	case 0:
		return ""
	case 1:
		return ":"
	case 2:
		return ":080"
	case 3:
		if strings.EqualFold(scheme, "https") {
			return ":443"
		}
		return ":80"
	default:
		return fmt.Sprintf(":%d", rng.Intn(9000))
	}
}

func randomPath(rng *rand.Rand) string {
	segs := []string{"a", "B", ".", "..", "%2e", "%2E%2E", "%41", "a%2Fb",
		"%2f", "", "x%zz", "%", "c d", "%E4%B8%AD", "%ff", "~", "%7e"}
	n := rng.Intn(6)
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteByte('/')
		b.WriteString(pick(rng, segs))
	}
	if rng.Intn(3) == 0 {
		b.WriteByte('/')
	}
	return b.String()
}

func randomQuery(rng *rand.Rand) string {
	keys := []string{"a", "b", "A", "%6B", "k%20"}
	vals := []string{"1", "2", "%20", "", "%2f", "%41", "x%zz", "%"}
	n := rng.Intn(5)
	parts := make([]string, 0, n)
	for i := 0; i < n; i++ {
		p := pick(rng, keys)
		switch rng.Intn(3) {
		case 0: // no '='
		case 1:
			p += "="
		default:
			p += "=" + pick(rng, vals)
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, "&")
}

func pick(rng *rand.Rand, xs []string) string { return xs[rng.Intn(len(xs))] }

func newRand() *rand.Rand { return rand.New(rand.NewSource(42)) }
