package release

import (
	"sort"
	"sync"
)

// Releaser 是并发安全的发版推导器。
type Releaser struct {
	mu       sync.Mutex
	tagged   []Version // 始终按 CompareVersion 升序、无重复
	tagIndex map[string]struct{}
}

// New 创建一个空的发版推导器。
func New() *Releaser {
	return &Releaser{tagIndex: map[string]struct{}{}}
}

// Tag 登记一个已发布版本。
// 版本串非法返回 ErrInvalidVersion，兼容不同规范化写法的重复登记。
func (r *Releaser) Tag(s string) error {
	v, err := ParseVersion(s)
	if err != nil {
		return fail(ErrInvalidVersion, "%q: %v", s, err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.tagIndex[v.String()]; ok {
		return fail(ErrAlreadyTagged, "%s already released", v.String())
	}
	r.addLocked(v)
	return nil
}

// Versions 返回全部已发布版本，按规定顺序升序排列。
// 返回切片为副本，调用方可自由修改。
func (r *Releaser) Versions() []Version {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Version, len(r.tagged))
	copy(out, r.tagged)
	return out
}

// Next 只读推导下一个版本，不改变已发布集合。
func (r *Releaser) Next(commits []Commit, channel string) (Version, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	snap := append([]Version(nil), r.tagged...)
	return derive(snap, commits, channel)
}

// Release 推导下一个版本并登记后返回。
func (r *Releaser) Release(commits []Commit, channel string) (Version, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	v, err := derive(r.tagged, commits, channel)
	if err != nil {
		return Version{}, err
	}
	r.addLocked(v)
	return v, nil
}

func (r *Releaser) addLocked(v Version) {
	pos := sort.Search(len(r.tagged), func(i int) bool {
		return CompareVersion(r.tagged[i], v) >= 0
	})
	r.tagged = append(r.tagged, Version{})
	copy(r.tagged[pos+1:], r.tagged[pos:])
	r.tagged[pos] = v
	r.tagIndex[v.String()] = struct{}{}
}

// Level 是一次提交映射出的升级级别。
type Level int

const (
	LevelNone Level = iota
	LevelPatch
	LevelMinor
	LevelMajor
)

func (l Level) String() string {
	switch l {
	case LevelPatch:
		return "patch"
	case LevelMinor:
		return "minor"
	case LevelMajor:
		return "major"
	default:
		return "none"
	}
}

// levelOf 按基线主版本决定级别：0.x 阶段 Major/Minor 各降一级。
func levelOf(c Commit, baseline Core) Level {
	if c.Breaking {
		if baseline.Major >= 1 {
			return LevelMajor
		}
		return LevelMinor
	}
	switch c.Type {
	case "feat":
		if baseline.Major >= 1 {
			return LevelMinor
		}
		return LevelPatch
	case "fix", "perf":
		return LevelPatch
	default:
		return LevelNone
	}
}

// bump 按级别升级核心，允许中间结果超过 MaxComponent（最终再检查）。
func bump(c Core, l Level) Core {
	switch l {
	case LevelMajor:
		return Core{c.Major + 1, 0, 0}
	case LevelMinor:
		return Core{c.Major, c.Minor + 1, 0}
	case LevelPatch:
		return Core{c.Major, c.Minor, c.Patch + 1}
	default:
		return c
	}
}

func coreInRange(c Core) bool {
	return c.Major <= MaxComponent && c.Minor <= MaxComponent && c.Patch <= MaxComponent
}

// derive 是纯函数式的推导核心：给定已发布快照与入参，返回结果或第一个错误。
func derive(tagged []Version, commits []Commit, channel string) (Version, error) {
	if channel != "" && !validChannel(channel) {
		return Version{}, fail(ErrInvalidChannel, "%q is not 1-16 lowercase letters", channel)
	}
	for i, c := range commits {
		if !validCommitType(c.Type) {
			return Version{}, fail(ErrInvalidCommitType, "commits[%d] type %q", i, c.Type)
		}
	}

	// 基线 S：最大的已发布稳定版本，没有则 0.0.0。
	baseline := Core{}
	hasBaseline := false
	// 预发布线 P：核心大于 S 的预发布版本中的最大核心。
	var prereleaseLine Core
	hasLine := false

	for _, v := range tagged {
		if v.Stable() && (!hasBaseline || CompareCore(v.Core, baseline) > 0) {
			baseline = v.Core
			hasBaseline = true
		}
	}
	for _, v := range tagged {
		if !v.Stable() && CompareCore(v.Core, baseline) > 0 &&
			(!hasLine || CompareCore(v.Core, prereleaseLine) > 0) {
			prereleaseLine = v.Core
			hasLine = true
		}
	}

	// 最大提交级别 L 与提交升级目标 T。
	maxLevel := LevelNone
	for _, c := range commits {
		if l := levelOf(c, baseline); l > maxLevel {
			maxLevel = l
		}
	}
	hasTarget := maxLevel != LevelNone

	var target Core
	if hasTarget {
		target = bump(baseline, maxLevel)
	}

	// 目标核心 K：T 与 P 取较大者；都没有则无可发布。
	var k Core
	switch {
	case hasTarget && hasLine:
		if CompareCore(target, prereleaseLine) >= 0 {
			k = target
		} else {
			k = prereleaseLine
		}
	case hasTarget:
		k = target
	case hasLine:
		k = prereleaseLine
	default:
		return Version{}, fail(ErrNoRelease, "no level-bearing commits and no prerelease line")
	}

	if !coreInRange(k) {
		return Version{}, fail(ErrOverflow, "target core %d.%d.%d exceeds %d", k.Major, k.Minor, k.Patch, MaxComponent)
	}

	result := Version{Core: k, Channel: channel}
	if channel != "" {
		maxN := 0
		for _, v := range tagged {
			if v.Channel == channel && CompareCore(v.Core, k) == 0 && v.N > maxN {
				maxN = v.N
			}
		}
		result.N = maxN + 1
		if result.N > MaxComponent {
			return Version{}, fail(ErrOverflow, "prerelease number %d exceeds %d", result.N, MaxComponent)
		}
	}
	return result, nil
}
