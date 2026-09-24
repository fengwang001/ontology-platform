package undo

import (
	"encoding/base64"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"strconv"
	"strings"

	"ontology/name"
	"ontology/plan"
)

var (
	ErrHeaderTruncated = errors.New("header truncated")
	ErrRecordTruncated = errors.New("record truncated")
	ErrCRCMismatch     = errors.New("CRC mismatch")
)

const headerLen = 18

type Result struct {
	Undone      int
	Recovered   int
	Unavailable int
	FirstError  error
}

func File(ns *name.Namespace, path string) (*Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	steps, res, err := Parse(data)
	if err != nil && res == nil {
		return nil, err
	}
	ns.WithLock(func(items map[string]struct{}) {
		for i := len(steps) - 1; i >= 0; i-- {
			from, to := steps[i].From, steps[i].To
			_, hasFrom := items[from]
			_, hasTo := items[to]
			if hasFrom && !hasTo {
				continue
			}
			if hasTo && !hasFrom {
				delete(items, to)
				items[from] = struct{}{}
				res.Undone++
			}
		}
	})
	return res, err
}

func Parse(data []byte) ([]plan.Step, *Result, error) {
	var steps []plan.Step
	pos := 0
	for pos < len(data) {
		if len(data)-pos < headerLen {
			return steps, result(0, 0, ErrHeaderTruncated), ErrHeaderTruncated
		}
		header := string(data[pos : pos+headerLen])
		crcText, rest1, ok1 := strings.Cut(header, "|")
		lengthText, _, ok2 := strings.Cut(rest1, "|")
		if !ok1 || !ok2 {
			return steps, result(0, 0, ErrHeaderTruncated), ErrHeaderTruncated
		}
		length, err := strconv.ParseInt(lengthText, 16, 64)
		if err != nil {
			return steps, result(0, 0, ErrHeaderTruncated), ErrHeaderTruncated
		}
		end := pos + headerLen + int(length)
		available := len(data) - pos - headerLen
		if available < int(length)-1 {
			return steps, result(len(steps), 0, ErrRecordTruncated), ErrRecordTruncated
		}
		if available < int(length) {
			return steps, result(len(steps), 1, ErrCRCMismatch), ErrCRCMismatch
		}
		record := data[pos+headerLen : end]
		if crc(record) != crcText {
			return steps, result(len(steps), 1, ErrCRCMismatch), ErrCRCMismatch
		}
		step, err := decode(record)
		if err != nil {
			return steps, result(len(steps), 0, ErrCRCMismatch), ErrCRCMismatch
		}
		steps = append(steps, step)
		pos = end
	}
	return steps, result(len(steps), 0, nil), nil
}

func result(recovered, unavailable int, err error) *Result {
	return &Result{Recovered: recovered, Unavailable: unavailable, FirstError: err}
}

func crc(record []byte) string {
	return fmt.Sprintf("%08x", crc32.ChecksumIEEE(record))
}

func decode(record []byte) (plan.Step, error) {
	body := strings.TrimSuffix(string(record), "\n")
	raw, err := base64.StdEncoding.DecodeString(body)
	if err != nil {
		return plan.Step{}, err
	}
	fields := strings.Split(string(raw), "\x00")
	if len(fields) != 3 {
		return plan.Step{}, ErrCRCMismatch
	}
	return plan.Step{From: fields[1], To: fields[2]}, nil
}
