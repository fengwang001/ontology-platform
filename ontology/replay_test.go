package ontology

import (
	"bytes"
	"reflect"
	"strconv"
	"testing"
)

func TestSameOperationSequenceReplaysIdentically(t *testing.T) {
	first := deterministicRun()
	second := deterministicRun()

	if !reflect.DeepEqual(first.results, second.results) {
		t.Fatalf("results differ:\nfirst=%v\nsecond=%v", first.results, second.results)
	}
	if first.output.String() != second.output.String() {
		t.Fatalf("logged operation traces differ")
	}
}

type replayOutput struct {
	output  *bytes.Buffer
	results []string
}

func deterministicRun() replayOutput {
	var output bytes.Buffer
	recorder := NewLogRecorder(NewStandardLogger(&output))
	bank := NewBank()
	bank.SetLogger(recorder)
	results := make([]string, 0, 16)

	results = append(results, errString(bank.CreateAccount("cash", 50, 0, 100, 3)))
	results = append(results, errString(bank.BeginTransaction(1)))
	results = append(results, errString(bank.BeginTransaction(2)))
	results = append(results, errString(bank.Reserve(1, "cash", -50)))
	results = append(results, errString(bank.Reserve(1, "cash", 50)))
	results = append(results, errString(bank.Reserve(2, "cash", 1)))
	results = append(results, errString(bank.Reserve(1, "cash", -1)))
	read, readErr := bank.Read("cash")
	results = append(results, readString(read, readErr))
	results = append(results, errString(bank.Commit(2)))
	results = append(results, errString(bank.Abort(1)))
	read, readErr = bank.Read("cash")
	results = append(results, readString(read, readErr))

	return replayOutput{output: &output, results: results}
}

func errString(err error) string {
	if err == nil {
		return "ok"
	}
	return err.Error()
}

func readString(result ReadResult, err error) string {
	if err != nil {
		return err.Error()
	}
	if result.Certain {
		return "balance:" + strconv.FormatInt(result.Balance, 10)
	}
	return "range:" + strconv.FormatInt(result.Lower, 10) + ":" + strconv.FormatInt(result.Upper, 10)
}
