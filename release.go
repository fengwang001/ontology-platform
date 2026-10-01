package ontology

import (
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
)

var (
	ErrInvalidVersion     = errors.New("invalid version")
	ErrDuplicateVersion   = errors.New("duplicate version")
	ErrInvalidChannel     = errors.New("invalid release channel")
	ErrInvalidCommitType  = errors.New("invalid commit type")
	ErrNoRelease          = errors.New("no release available")
	ErrCoreOverflow       = errors.New("version core overflow")
	ErrPrereleaseOverflow = errors.New("prerelease number overflow")
)

const maxVersionPart = 999999

type Version struct {
	Major      int
	Minor      int
	Patch      int
	Channel    string
	Prerelease int
}

type level int

const (
	levelNone level = iota
	levelPatch
	levelMinor
	levelMajor
)

type Commit struct {
	Type     string
	Breaking bool
}

func (v Version) String() string {
	core := strconv.Itoa(v.Major) + "." + strconv.Itoa(v.Minor) + "." + strconv.Itoa(v.Patch)
	if v.Channel == "" {
		return core
	}
	return core + "-" + v.Channel + "." + strconv.Itoa(v.Prerelease)
}

type Releaser struct {
	mu       sync.RWMutex
	released map[Version]struct{}
}

func NewReleaser() *Releaser {
	return &Releaser{released: make(map[Version]struct{})}
}

func ParseVersion(text string) (Version, error) {
	parts := strings.Split(text, "-")
	if len(parts) > 2 {
		return Version{}, ErrInvalidVersion
	}

	coreParts := strings.Split(parts[0], ".")
	if len(coreParts) != 3 {
		return Version{}, ErrInvalidVersion
	}

	numeric := make([]int, 3)
	for index, part := range coreParts {
		value, ok := parseVersionNumber(part)
		if !ok {
			return Version{}, ErrInvalidVersion
		}
		numeric[index] = value
	}

	version := Version{
		Major: numeric[0],
		Minor: numeric[1],
		Patch: numeric[2],
	}

	if len(parts) == 2 {
		preParts := strings.Split(parts[1], ".")
		if len(preParts) != 2 || !validChannel(preParts[0]) {
			return Version{}, ErrInvalidVersion
		}
		number, ok := parseVersionNumber(preParts[1])
		if !ok || number < 1 {
			return Version{}, ErrInvalidVersion
		}
		version.Channel = preParts[0]
		version.Prerelease = number
	}

	return version, nil
}

func parseVersionNumber(text string) (int, bool) {
	if text == "" || len(text) > 1 && text[0] == '0' {
		return 0, false
	}
	for _, char := range text {
		if char < '0' || char > '9' {
			return 0, false
		}
	}
	value, err := strconv.Atoi(text)
	if err != nil || value < 0 || value > maxVersionPart {
		return 0, false
	}
	return value, true
}

func validChannel(channel string) bool {
	if len(channel) < 1 || len(channel) > 16 {
		return false
	}
	for _, char := range channel {
		if char < 'a' || char > 'z' {
			return false
		}
	}
	return true
}

func validCommitType(commitType string) bool {
	if commitType == "" {
		return false
	}
	for _, char := range commitType {
		if char < 'a' || char > 'z' {
			return false
		}
	}
	return true
}

func compareVersion(left, right Version) int {
	if result := compareCore(left, right); result != 0 {
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
	if !leftPre {
		return 0
	}
	if result := strings.Compare(left.Channel, right.Channel); result != 0 {
		return result
	}
	switch {
	case left.Prerelease < right.Prerelease:
		return -1
	case left.Prerelease > right.Prerelease:
		return 1
	default:
		return 0
	}
}

func compareCore(left, right Version) int {
	leftCore := [3]int{left.Major, left.Minor, left.Patch}
	rightCore := [3]int{right.Major, right.Minor, right.Patch}
	for index := range leftCore {
		switch {
		case leftCore[index] < rightCore[index]:
			return -1
		case leftCore[index] > rightCore[index]:
			return 1
		}
	}
	return 0
}

func (r *Releaser) Tag(version string) error {
	parsed, err := ParseVersion(version)
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.released[parsed]; exists {
		return ErrDuplicateVersion
	}
	r.released[parsed] = struct{}{}
	return nil
}

func (r *Releaser) Versions() []Version {
	r.mu.RLock()
	defer r.mu.RUnlock()
	versions := make([]Version, 0, len(r.released))
	for version := range r.released {
		versions = append(versions, version)
	}
	sort.Slice(versions, func(i, j int) bool {
		return compareVersion(versions[i], versions[j]) < 0
	})
	return versions
}

func (r *Releaser) Next(commits []Commit, channel string) (Version, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.derive(commits, channel)
}

func (r *Releaser) Release(commits []Commit, channel string) (Version, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	version, err := r.derive(commits, channel)
	if err != nil {
		return Version{}, err
	}
	r.released[version] = struct{}{}
	return version, nil
}

func (r *Releaser) derive(commits []Commit, channel string) (Version, error) {
	if channel != "" && !validChannel(channel) {
		return Version{}, ErrInvalidChannel
	}
	for _, commit := range commits {
		if !validCommitType(commit.Type) {
			return Version{}, ErrInvalidCommitType
		}
	}

	baseline := Version{}
	hasBaseline := false
	var prereleaseCore Version
	hasPrereleaseLine := false

	for version := range r.released {
		if version.Channel == "" {
			if !hasBaseline || compareVersion(version, baseline) > 0 {
				baseline = version
				hasBaseline = true
			}
		}
	}

	for version := range r.released {
		if version.Channel == "" {
			continue
		}
		if compareCore(version, baseline) > 0 &&
			(!hasPrereleaseLine || compareCore(version, prereleaseCore) > 0) {
			prereleaseCore = version
			hasPrereleaseLine = true
		}
	}

	highest := levelNone
	for _, commit := range commits {
		highest = max(highest, commitLevel(commit, baseline.Major))
	}

	var target Version
	hasTarget := false
	if highest != levelNone {
		target = bumpCore(baseline, highest)
		hasTarget = true
	}
	if hasPrereleaseLine {
		if !hasTarget || compareCore(prereleaseCore, target) > 0 {
			target = prereleaseCore
		}
		hasTarget = true
	}
	if !hasTarget {
		return Version{}, ErrNoRelease
	}
	if target.Major > maxVersionPart || target.Minor > maxVersionPart || target.Patch > maxVersionPart {
		return Version{}, ErrCoreOverflow
	}

	result := Version{Major: target.Major, Minor: target.Minor, Patch: target.Patch}
	if channel != "" {
		result.Channel = channel
		result.Prerelease = 1
		for version := range r.released {
			if version.Channel == channel && compareCore(version, target) == 0 &&
				version.Prerelease >= result.Prerelease {
				result.Prerelease = version.Prerelease + 1
			}
		}
		if result.Prerelease > maxVersionPart {
			return Version{}, ErrPrereleaseOverflow
		}
	}
	return result, nil
}

func commitLevel(commit Commit, baselineMajor int) level {
	if commit.Breaking {
		if baselineMajor >= 1 {
			return levelMajor
		}
		return levelMinor
	}
	switch commit.Type {
	case "feat":
		if baselineMajor >= 1 {
			return levelMinor
		}
		return levelPatch
	case "fix", "perf":
		return levelPatch
	default:
		return levelNone
	}
}

func bumpCore(core Version, bump level) Version {
	switch bump {
	case levelMajor:
		return Version{Major: core.Major + 1}
	case levelMinor:
		return Version{Major: core.Major, Minor: core.Minor + 1}
	case levelPatch:
		return Version{Major: core.Major, Minor: core.Minor, Patch: core.Patch + 1}
	default:
		return core
	}
}
