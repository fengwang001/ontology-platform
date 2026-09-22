package spill

import (
	"io"
	"os"

	"ontology/record"
)

// Recovery is the classified outcome of scanning a possibly damaged run.
type Recovery struct {
	// Header is valid when HeaderErr is nil.
	Header Header
	// Records are every fully intact record, in order: the maximum
	// recoverable prefix. A half-written trailing record is excluded.
	Records []record.Record
	// HeaderErr is non-nil when the header itself is unusable.
	HeaderErr error
	// TailErr classifies why scanning stopped: io.EOF means a clean
	// complete run; otherwise one of ErrLengthPrefixIncomplete,
	// ErrRecordIncomplete, ErrCRC or ErrTrailingData.
	TailErr error
}

// Clean reports whether the run was fully intact.
func (rc Recovery) Clean() bool { return rc.HeaderErr == nil && rc.TailErr == io.EOF }

// Recover opens a possibly truncated/corrupted run file and extracts
// the largest intact prefix together with a definitive classification.
func Recover(path string) Recovery {
	var out Recovery
	info, err := os.Stat(path)
	if err != nil {
		out.HeaderErr = err
		return out
	}
	if info.Size() == 0 {
		out.HeaderErr = ErrEmptyFile
		return out
	}
	rd, err := Open(path)
	if err != nil {
		out.HeaderErr = err
		return out
	}
	defer rd.Close()
	out.Header = rd.h
	for {
		rec, err := rd.ReadRecord()
		if err == io.EOF {
			out.TailErr = io.EOF
			return out
		}
		if err != nil {
			out.TailErr = err
			return out
		}
		out.Records = append(out.Records, rec)
	}
}
