package chat

import "sort"

// message is the stored form of one channel message. The sequence number is
// implicit: messages are kept in a dense slice indexed by seq-1.
type message struct {
	author   string
	body     string
	mentions []string // sorted, unique
	sentAt   int64
	edits    int

	recalled   bool
	recalledBy string
	recalledAt int64
}

// MessageView is the read model returned by Fetch. A recalled message is
// exposed as a placeholder carrying only Seq, Recalled, RecalledBy and
// RecalledAt; every other field is zero.
type MessageView struct {
	Seq        int
	Author     string
	Body       string
	Mentions   []string
	SentAt     int64
	EditCount  int
	Recalled   bool
	RecalledBy string
	RecalledAt int64
}

func viewOf(seq int, m *message) MessageView {
	if m.recalled {
		return MessageView{
			Seq:        seq,
			Recalled:   true,
			RecalledBy: m.recalledBy,
			RecalledAt: m.recalledAt,
		}
	}
	mentions := make([]string, len(m.mentions))
	copy(mentions, m.mentions)
	return MessageView{
		Seq:       seq,
		Author:    m.author,
		Body:      m.body,
		Mentions:  mentions,
		SentAt:    m.sentAt,
		EditCount: m.edits,
	}
}

// sortedUnique returns a sorted copy of ids. The input must already be
// duplicate-free (validated by the caller).
func sortedUnique(ids []string) []string {
	out := make([]string, len(ids))
	copy(out, ids)
	sort.Strings(out)
	return out
}

func containsString(sorted []string, id string) bool {
	i := sort.SearchStrings(sorted, id)
	return i < len(sorted) && sorted[i] == id
}
