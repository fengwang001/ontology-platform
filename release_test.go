package ontology

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func mustTag(t *testing.T, r *Releaser, version string) Version {
	t.Helper()
	parsed, err := ParseVersion(version)
	if err != nil {
		t.Fatalf("ParseVersion(%q): %v", version, err)
	}
	if err := r.Tag(version); err != nil {
		t.Fatalf("Tag(%q): %v", version, err)
	}
	return parsed
}

func TestDocumentedExamples(t *testing.T) {
	r := NewReleaser()
	mustTag(t, r, "1.2.3")
	mustTag(t, r, "2.0.0-rc.1")

	got, err := r.Next([]Commit{{Type: "fix"}}, "rc")
	if err != nil {
		t.Fatalf("Next rc: %v", err)
	}
	if got.String() != "2.0.0-rc.2" {
		t.Fatalf("Next rc = %s", got)
	}

	got, err = r.Next([]Commit{{Type: "fix"}}, "")
	if err != nil {
		t.Fatalf("Next stable: %v", err)
	}
	if got.String() != "2.0.0" {
		t.Fatalf("Next stable = %s", got)
	}

	r = NewReleaser()
	mustTag(t, r, "0.4.0")
	got, err = r.Next([]Commit{{Type: "feat"}, {Type: "fix", Breaking: true}}, "")
	if err != nil {
		t.Fatalf("Next 0.x: %v", err)
	}
	if got.String() != "0.5.0" {
		t.Fatalf("Next 0.x = %s", got)
	}
}

func TestLevelMapping(t *testing.T) {
	tests := []struct {
		baseline string
		commit   Commit
		want     string
	}{
		{"1.0.0", Commit{Type: "chore", Breaking: true}, "2.0.0"},
		{"1.0.0", Commit{Type: "feat"}, "1.1.0"},
		{"1.0.0", Commit{Type: "fix"}, "1.0.1"},
		{"1.0.0", Commit{Type: "perf"}, "1.0.1"},
		{"1.0.0", Commit{Type: "docs"}, ""},
		{"0.1.0", Commit{Type: "feat", Breaking: true}, "0.2.0"},
		{"0.1.0", Commit{Type: "feat"}, "0.1.1"},
		{"0.1.0", Commit{Type: "fix"}, "0.1.1"},
		{"0.1.0", Commit{Type: "perf"}, "0.1.1"},
		{"0.1.0", Commit{Type: "docs"}, ""},
	}

	for _, tc := range tests {
		name := tc.baseline + "/" + tc.commit.Type + strconv.FormatBool(tc.commit.Breaking)
		t.Run(name, func(t *testing.T) {
			r := NewReleaser()
			mustTag(t, r, tc.baseline)
			got, err := r.Next([]Commit{tc.commit}, "")
			if tc.want == "" {
				if !errors.Is(err, ErrNoRelease) {
					t.Fatalf("err = %v, want ErrNoRelease", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Next: %v", err)
			}
			if got.String() != tc.want {
				t.Fatalf("Next = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestVersionsOrderingAndBaseline(t *testing.T) {
	r := NewReleaser()
	for _, version := range []string{
		"1.0.0",
		"0.9.0",
		"1.0.1-beta.2",
		"1.0.1-alpha.9",
		"1.0.1-alpha.10",
		"1.0.1-beta.1",
		"1.0.1",
	} {
		mustTag(t, r, version)
	}

	got := r.Versions()
	want := []string{
		"0.9.0",
		"1.0.0",
		"1.0.1-alpha.9",
		"1.0.1-alpha.10",
		"1.0.1-beta.1",
		"1.0.1-beta.2",
		"1.0.1",
	}
	if len(got) != len(want) {
		t.Fatalf("Versions len = %d, want %d", len(got), len(want))
	}
	for index := range want {
		if got[index].String() != want[index] {
			t.Fatalf("Versions[%d] = %s, want %s; full=%v", index, got[index], want[index], got)
		}
	}
}

func TestBaselineIgnoresPublicationOrder(t *testing.T) {
	r := NewReleaser()
	mustTag(t, r, "2.0.0")
	mustTag(t, r, "1.9.9")
	got, err := r.Next([]Commit{{Type: "fix"}}, "")
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if got.String() != "2.0.1" {
		t.Fatalf("Next = %s, want 2.0.1", got)
	}
}

func TestPrereleaseLineAndNoLevelCommits(t *testing.T) {
	r := NewReleaser()
	mustTag(t, r, "1.0.0")
	mustTag(t, r, "2.0.0-beta.1")

	got, err := r.Next([]Commit{{Type: "docs"}}, "")
	if err != nil {
		t.Fatalf("Next stable on prerelease line: %v", err)
	}
	if got.String() != "2.0.0" {
		t.Fatalf("Next = %s, want 2.0.0", got)
	}

	r = NewReleaser()
	mustTag(t, r, "1.0.0")
	_, err = r.Next([]Commit{{Type: "docs"}}, "")
	if !errors.Is(err, ErrNoRelease) {
		t.Fatalf("err = %v, want ErrNoRelease", err)
	}
}

func TestPrereleaseLineOnlyMovesUp(t *testing.T) {
	r := NewReleaser()
	mustTag(t, r, "1.5.0")
	mustTag(t, r, "1.4.0-rc.1")
	got, err := r.Next([]Commit{{Type: "fix"}}, "rc")
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if got.String() != "1.5.1-rc.1" {
		t.Fatalf("Next = %s, want 1.5.1-rc.1", got)
	}

	mustTag(t, r, "2.0.0-alpha.3")
	got, err = r.Next([]Commit{{Type: "fix"}}, "rc")
	if err != nil {
		t.Fatalf("Next with larger line: %v", err)
	}
	if got.String() != "2.0.0-rc.1" {
		t.Fatalf("Next = %s, want 2.0.0-rc.1", got)
	}
}

func TestChannelsCountIndependently(t *testing.T) {
	r := NewReleaser()
	mustTag(t, r, "1.0.0")
	mustTag(t, r, "1.1.0-alpha.2")
	mustTag(t, r, "1.1.0-beta.4")

	alpha, err := r.Next([]Commit{{Type: "feat"}}, "alpha")
	if err != nil {
		t.Fatalf("alpha Next: %v", err)
	}
	beta, err := r.Next([]Commit{{Type: "feat"}}, "beta")
	if err != nil {
		t.Fatalf("beta Next: %v", err)
	}
	gamma, err := r.Next([]Commit{{Type: "feat"}}, "gamma")
	if err != nil {
		t.Fatalf("gamma Next: %v", err)
	}
	if alpha.String() != "1.1.0-alpha.3" || beta.String() != "1.1.0-beta.5" || gamma.String() != "1.1.0-gamma.1" {
		t.Fatalf("channels = %s, %s, %s", alpha, beta, gamma)
	}
}

func TestOverflowAndSupersededTarget(t *testing.T) {
	r := NewReleaser()
	mustTag(t, r, "999999.999999.999999")
	_, err := r.Next([]Commit{{Type: "fix"}}, "")
	if !errors.Is(err, ErrCoreOverflow) {
		t.Fatalf("patch overflow err = %v", err)
	}

	r = NewReleaser()
	mustTag(t, r, "999999.0.0")
	_, err = r.Next([]Commit{{Type: "chore", Breaking: true}}, "")
	if !errors.Is(err, ErrCoreOverflow) {
		t.Fatalf("major overflow err = %v", err)
	}

	r = NewReleaser()
	mustTag(t, r, "1.999999.0")
	_, err = r.Next([]Commit{{Type: "feat"}}, "")
	if !errors.Is(err, ErrCoreOverflow) {
		t.Fatalf("minor overflow err = %v", err)
	}

	r = NewReleaser()
	mustTag(t, r, "1.999999.0")
	mustTag(t, r, "2.0.0-rc.1")
	got, err := r.Next([]Commit{{Type: "feat"}}, "rc")
	if err != nil {
		t.Fatalf("larger prerelease line should supersede overflow target: %v", err)
	}
	if got.String() != "2.0.0-rc.2" {
		t.Fatalf("Next = %s", got)
	}

	r = NewReleaser()
	mustTag(t, r, "1.0.0")
	mustTag(t, r, "1.1.0-rc.999999")
	_, err = r.Next([]Commit{{Type: "feat"}}, "rc")
	if !errors.Is(err, ErrPrereleaseOverflow) {
		t.Fatalf("prerelease overflow err = %v", err)
	}
}

func TestRejectionPriorityAndAtomicity(t *testing.T) {
	invalidVersions := []string{
		"", "1", "1.2", "v1.2.3", "01.2.3", "1.2.3-",
		"1.2.3-RC.1", "1.2.3-rc.0", "1.2.3-rc.01", "1.2.3-rc.1000000",
		"1000000.0.0", "1.2.3-.1", "1.2.3-abcdefghijklmnopq.1",
	}
	for _, version := range invalidVersions {
		r := NewReleaser()
		if err := r.Tag(version); !errors.Is(err, ErrInvalidVersion) {
			t.Fatalf("Tag(%q) err = %v, want ErrInvalidVersion", version, err)
		}
	}

	r := NewReleaser()
	mustTag(t, r, "1.0.0")
	if err := r.Tag("1.0.0"); !errors.Is(err, ErrDuplicateVersion) {
		t.Fatalf("duplicate Tag err = %v", err)
	}

	_, err := r.Next([]Commit{{Type: "bad"}, {Type: "also-bad"}}, "Bad")
	if !errors.Is(err, ErrInvalidChannel) {
		t.Fatalf("channel should be checked first, err = %v", err)
	}
	_, err = r.Next([]Commit{{Type: "docs"}, {Type: "bad"}}, "Bad2")
	if !errors.Is(err, ErrInvalidChannel) {
		t.Fatalf("invalid channel err = %v", err)
	}
	_, err = r.Next([]Commit{{Type: "docs"}, {Type: "bad-type"}}, "rc")
	if !errors.Is(err, ErrInvalidCommitType) {
		t.Fatalf("invalid commit err = %v", err)
	}
	_, err = r.Next(nil, "")
	if !errors.Is(err, ErrNoRelease) {
		t.Fatalf("no release err = %v", err)
	}

	before := len(r.Versions())
	_, err = r.Release([]Commit{{Type: "bad"}}, "Bad")
	if err == nil {
		t.Fatal("invalid Release succeeded")
	}
	if len(r.Versions()) != before {
		t.Fatalf("rejected Release changed versions: before=%d after=%d", before, len(r.Versions()))
	}
}

func TestConcurrentIdenticalPrereleases(t *testing.T) {
	r := NewReleaser()
	mustTag(t, r, "1.0.0")

	const count = 32
	start := make(chan struct{})
	results := make(chan string, count)
	var wg sync.WaitGroup
	for index := 0; index < count; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			version, err := r.Release([]Commit{{Type: "feat"}}, "rc")
			if err != nil {
				t.Errorf("concurrent Release: %v", err)
				return
			}
			results <- version.String()
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	seen := map[string]bool{}
	numbers := map[int]bool{}
	for result := range results {
		if seen[result] {
			t.Fatalf("duplicate concurrent result %q", result)
		}
		seen[result] = true
		parts := strings.Split(result, ".")
		number, err := strconv.Atoi(parts[len(parts)-1])
		if err != nil {
			t.Fatalf("parse result %q: %v", result, err)
		}
		numbers[number] = true
	}
	for number := 1; number <= count; number++ {
		if !numbers[number] {
			t.Fatalf("missing prerelease number %d in %v", number, seen)
		}
	}
}

type oracleState struct {
	versions []Version
}

type oracleDecision struct {
	version        Version
	err            error
	baseline       string
	prereleaseLine string
	highestLevel   level
	commitTarget   string
}

func (s *oracleState) tag(version Version) error {
	for _, existing := range s.versions {
		if oracleCompare(existing, version) == 0 {
			return ErrDuplicateVersion
		}
	}
	s.versions = append(s.versions, version)
	sort.Slice(s.versions, func(i, j int) bool {
		return oracleCompare(s.versions[i], s.versions[j]) < 0
	})
	return nil
}

func (s oracleState) derive(commits []Commit, channel string) oracleDecision {
	if channel != "" && !validChannel(channel) {
		return oracleDecision{err: ErrInvalidChannel}
	}
	for _, commit := range commits {
		if !validCommitType(commit.Type) {
			return oracleDecision{err: ErrInvalidCommitType}
		}
	}

	baseline := Version{}
	hasBaseline := false
	for _, version := range s.versions {
		if version.Channel == "" && (!hasBaseline || oracleCompare(version, baseline) > 0) {
			baseline = version
			hasBaseline = true
		}
	}

	prereleaseLine := Version{}
	hasPrereleaseLine := false
	for _, version := range s.versions {
		if version.Channel != "" && oracleCompareCore(version, baseline) > 0 &&
			(!hasPrereleaseLine || oracleCompareCore(version, prereleaseLine) > 0) {
			prereleaseLine = version
			hasPrereleaseLine = true
		}
	}

	highest := levelNone
	for _, commit := range commits {
		highest = max(highest, oracleCommitLevel(commit, baseline.Major))
	}

	target := Version{}
	hasTarget := false
	if highest != levelNone {
		target = oracleBumpCore(baseline, highest)
		hasTarget = true
	}
	commitTarget := ""
	if highest != levelNone {
		commitTarget = fmt.Sprintf("%d.%d.%d", target.Major, target.Minor, target.Patch)
	}
	if hasPrereleaseLine && (!hasTarget || oracleCompareCore(prereleaseLine, target) > 0) {
		target = Version{
			Major: prereleaseLine.Major,
			Minor: prereleaseLine.Minor,
			Patch: prereleaseLine.Patch,
		}
		hasTarget = true
	}
	if !hasTarget {
		decision := s.decisionMeta(Version{}, ErrNoRelease, baseline, prereleaseLine, highest, commitTarget)
		return decision
	}
	if target.Major > maxVersionPart || target.Minor > maxVersionPart || target.Patch > maxVersionPart {
		return s.decisionMeta(Version{}, ErrCoreOverflow, baseline, prereleaseLine, highest, commitTarget)
	}

	result := Version{Major: target.Major, Minor: target.Minor, Patch: target.Patch}
	if channel == "" {
		return s.decisionMeta(result, nil, baseline, prereleaseLine, highest, commitTarget)
	}
	result.Channel = channel
	result.Prerelease = 1
	for _, version := range s.versions {
		if version.Channel == channel && oracleCompareCore(version, target) == 0 &&
			version.Prerelease >= result.Prerelease {
			result.Prerelease = version.Prerelease + 1
		}
	}
	if result.Prerelease > maxVersionPart {
		return s.decisionMeta(Version{}, ErrPrereleaseOverflow, baseline, prereleaseLine, highest, commitTarget)
	}
	return s.decisionMeta(result, nil, baseline, prereleaseLine, highest, commitTarget)
}

func (s oracleState) decisionMeta(
	version Version,
	err error,
	baseline Version,
	prereleaseLine Version,
	highest level,
	commitTarget string,
) oracleDecision {
	prereleaseText := ""
	if prereleaseLine.Major != 0 || prereleaseLine.Minor != 0 || prereleaseLine.Patch != 0 ||
		prereleaseLine.Channel != "" || prereleaseLine.Prerelease != 0 {
		prereleaseText = fmt.Sprintf("%d.%d.%d",
			prereleaseLine.Major, prereleaseLine.Minor, prereleaseLine.Patch)
	}
	return oracleDecision{
		version:        version,
		err:            err,
		baseline:       baseline.String(),
		prereleaseLine: prereleaseText,
		highestLevel:   highest,
		commitTarget:   commitTarget,
	}
}

func oracleCompare(left, right Version) int {
	if result := oracleCompareCore(left, right); result != 0 {
		return result
	}
	leftPre := left.Channel != ""
	rightPre := right.Channel != ""
	if leftPre != rightPre {
		if leftPre {
			return -1
		}
		return 1
	}
	if left.Channel < right.Channel {
		return -1
	}
	if left.Channel > right.Channel {
		return 1
	}
	if left.Prerelease < right.Prerelease {
		return -1
	}
	if left.Prerelease > right.Prerelease {
		return 1
	}
	return 0
}

func oracleCompareCore(left, right Version) int {
	leftCore := [3]int{left.Major, left.Minor, left.Patch}
	rightCore := [3]int{right.Major, right.Minor, right.Patch}
	for index := range leftCore {
		if leftCore[index] < rightCore[index] {
			return -1
		}
		if leftCore[index] > rightCore[index] {
			return 1
		}
	}
	return 0
}

func oracleCommitLevel(commit Commit, baselineMajor int) level {
	if commit.Breaking {
		if baselineMajor > 0 {
			return levelMajor
		}
		return levelMinor
	}
	if commit.Type == "feat" {
		if baselineMajor > 0 {
			return levelMinor
		}
		return levelPatch
	}
	if commit.Type == "fix" || commit.Type == "perf" {
		return levelPatch
	}
	return levelNone
}

func oracleBumpCore(core Version, bump level) Version {
	if bump == levelMajor {
		return Version{Major: core.Major + 1}
	}
	if bump == levelMinor {
		return Version{Major: core.Major, Minor: core.Minor + 1}
	}
	if bump == levelPatch {
		return Version{Major: core.Major, Minor: core.Minor, Patch: core.Patch + 1}
	}
	return core
}

func TestRandomDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(20261001))
	commitTypes := []string{"feat", "fix", "perf", "docs", "chore", "bad-type", ""}
	channels := []string{"", "rc", "alpha", "beta", "abcdefghijklmnop", "BAD"}

	for iteration := 0; iteration < 2000; iteration++ {
		r := NewReleaser()
		oracle := oracleState{}
		inputs := make([]string, 0, 12)
		type releaseOperation struct {
			commits []Commit
			channel string
			version Version
			err     error
		}
		releaseOperations := make([]releaseOperation, 0, 8)

		versionCount := rng.Intn(9)
		if rng.Intn(8) == 0 {
			versionCount = 1
		}
		for index := 0; index < versionCount; index++ {
			parsed := randomVersion(rng, iteration)
			version := parsed.String()
			_, parseErr := ParseVersion(version)
			oracleErr := oracle.tag(parsed)
			actualErr := r.Tag(version)
			if parseErr != nil {
				if actualErr == nil {
					t.Fatalf("case %d Tag(%q): expected parse error", iteration, version)
				}
				continue
			}
			if !errors.Is(oracleErr, actualErr) || !errors.Is(actualErr, oracleErr) {
				t.Fatalf("case %d Tag(%q): parse err=%v actual=%v", iteration, version, parseErr, actualErr)
			}
			if oracleErr == nil {
				inputs = append(inputs, "tag "+version)
			}
		}

		operationCount := 3 + rng.Intn(6)
		for operation := 0; operation < operationCount; operation++ {
			commitCount := rng.Intn(5)
			commits := make([]Commit, commitCount)
			commitText := make([]string, commitCount)
			for index := range commits {
				commits[index] = Commit{
					Type:     commitTypes[rng.Intn(len(commitTypes))],
					Breaking: rng.Intn(4) == 0,
				}
				commitText[index] = fmt.Sprintf("%s/breaking=%t", commits[index].Type, commits[index].Breaking)
			}
			channel := channels[rng.Intn(len(channels))]

			expectedDecision := oracle.derive(commits, channel)
			expected := expectedDecision.version
			expectedErr := expectedDecision.err
			actualNext, actualNextErr := r.Next(commits, channel)
			if !sameVersionError(expected, expectedErr, actualNext, actualNextErr) {
				t.Fatalf("case %d Next mismatch: expected=%q/%v actual=%q/%v",
					iteration, expected, expectedErr, actualNext, actualNextErr)
			}

			actual, actualErr := r.Release(commits, channel)
			if !sameVersionError(expected, expectedErr, actual, actualErr) {
				t.Fatalf("case %d Release mismatch: expected=%q/%v actual=%q/%v",
					iteration, expected, expectedErr, actual, actualErr)
			}
			if expectedErr == nil {
				if err := oracle.tag(expected); err != nil {
					t.Fatalf("case %d oracle tag result %s: %v", iteration, expected, err)
				}
			}
			releaseOperations = append(releaseOperations, releaseOperation{
				commits: commits,
				channel: channel,
				version: expected,
				err:     expectedErr,
			})

			actualVersions := r.Versions()
			if len(actualVersions) != len(oracle.versions) {
				t.Fatalf("case %d versions len = %d, want %d", iteration, len(actualVersions), len(oracle.versions))
			}
			for index := range actualVersions {
				if compareVersion(actualVersions[index], oracle.versions[index]) != 0 {
					t.Fatalf("case %d versions[%d] = %s, want %s", iteration, index, actualVersions[index], oracle.versions[index])
				}
			}

			decision := "no-release"
			if expectedErr == nil {
				decision = "result=" + expected.String()
			} else {
				decision = "error=" + expectedErr.Error()
			}
			t.Logf("case=%d tags=%v commits=%v channel=%q baseline=%s prerelease-line=%s highest-level=%d commit-target=%s %s",
				iteration, inputs, commitText, channel,
				expectedDecision.baseline, expectedDecision.prereleaseLine,
				expectedDecision.highestLevel, expectedDecision.commitTarget, decision)
		}

		replay := NewReleaser()
		for _, input := range inputs {
			version := strings.TrimPrefix(input, "tag ")
			if err := replay.Tag(version); err != nil {
				t.Fatalf("case %d replay Tag(%q): %v", iteration, version, err)
			}
		}
		for _, operation := range releaseOperations {
			replayed, err := replay.Release(operation.commits, operation.channel)
			if !sameVersionError(operation.version, operation.err, replayed, err) {
				t.Fatalf("case %d replay Release commits=%v channel=%q: expected=%q/%v actual=%q/%v",
					iteration, operation.commits, operation.channel, operation.version, operation.err, replayed, err)
			}
		}
		replayCount := len(replay.Versions())
		if replayCount != len(r.Versions()) {
			t.Fatalf("case %d replay versions = %d, want %d", iteration, replayCount, len(r.Versions()))
		}
	}
}

func randomVersion(rng *rand.Rand, iteration int) Version {
	coreIndex := rng.Intn(20)
	if iteration%17 == 0 && coreIndex == 0 {
		return Version{Major: maxVersionPart, Minor: maxVersionPart, Patch: maxVersionPart}
	}

	core := [3]int{
		rng.Intn(5),
		rng.Intn(5),
		rng.Intn(5),
	}
	if coreIndex == 1 {
		core[1] = maxVersionPart
	}
	version := Version{Major: core[0], Minor: core[1], Patch: core[2]}
	if rng.Intn(2) == 0 {
		version.Channel = []string{"rc", "alpha", "beta"}[rng.Intn(3)]
		if rng.Intn(25) == 0 {
			version.Prerelease = maxVersionPart
		} else {
			version.Prerelease = 1 + rng.Intn(4)
		}
	}
	return version
}

func sameVersionError(expected Version, expectedErr error, actual Version, actualErr error) bool {
	if expectedErr != nil || actualErr != nil {
		return errors.Is(expectedErr, actualErr) && errors.Is(actualErr, expectedErr)
	}
	return compareVersion(expected, actual) == 0
}
