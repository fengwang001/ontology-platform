package track

import (
	"errors"
	"fmt"
	"sync"

	"ontology/cue"
	"ontology/edit"
)

var (
	// ErrInvalidArgument：标识为空、数值越界、剪辑表不合规范、字幕集合构造参数非法。
	ErrInvalidArgument = errors.New("track: invalid argument")
	ErrTrackNotFound   = errors.New("track: track not found")
	ErrTrackExists     = errors.New("track: track already exists")
	ErrVersionNotFound = errors.New("track: version not found")
	// ErrConflict：base 非最新，或变基路径上存在普通提交。
	ErrConflict = errors.New("track: version conflict")
)

const DefaultDMin int64 = 500

// Version 是某一历史版本的不可变快照。
type Version struct {
	Number int
	Cues   []cue.Cue
}

type entry struct {
	kind  uint8 // 0 普通提交，1 重定时提交
	cues  []cue.Cue
	table *edit.Compiled // 仅 kind==1 时有效
}

type track struct {
	dmin int64
	vers []entry // vers[i] 为版本 i（vers[0] 为空初始版本）
}

var (
	mu     sync.Mutex
	tracks = map[string]*track{}
)

// CreateTrack 建一条空轨，初始版本号为 0、内容为空。
// 重复 id 报 ErrTrackExists；id 为空或 Dmin 越界报 ErrInvalidArgument。
func CreateTrack(id string, dmin ...int64) error {
	d := DefaultDMin
	if len(dmin) > 1 {
		return ErrInvalidArgument
	}
	if len(dmin) == 1 {
		d = dmin[0]
	}
	if id == "" || d < cue.MinDMin || d > cue.MaxDMin {
		return ErrInvalidArgument
	}
	mu.Lock()
	defer mu.Unlock()
	if _, ok := tracks[id]; ok {
		return ErrTrackExists
	}
	tracks[id] = &track{dmin: d, vers: []entry{{kind: 0}}}
	return nil
}

func getTrack(id string) (*track, error) {
	tr, ok := tracks[id]
	if !ok {
		return nil, ErrTrackNotFound
	}
	return tr, nil
}

// Commit 在 base 为最新版本时提交一份字幕，产生普通提交版本。
func Commit(id string, base int, cues []cue.Cue) (int, error) {
	if id == "" || base < 0 {
		return 0, ErrInvalidArgument
	}
	mu.Lock()
	defer mu.Unlock()
	tr, err := getTrack(id)
	if err != nil {
		return 0, err
	}
	if base > len(tr.vers)-1 {
		return 0, ErrVersionNotFound
	}
	if base != len(tr.vers)-1 {
		return 0, ErrConflict
	}
	if err := cue.Validate(cues, tr.dmin); err != nil {
		return 0, wrapCue(err)
	}
	tr.vers = append(tr.vers, entry{kind: 0, cues: cloneCues(cues)})
	return len(tr.vers) - 1, nil
}

// Retime 在 base 为最新版本时，把最新字幕按剪辑表重定时，产生重定时版本。
// 返回新版本号、Splits（内部插入切分次数）与 Dropped（丢弃片数）。
func Retime(id string, base int, table edit.Table) (int, int, int, error) {
	if id == "" || base < 0 {
		return 0, 0, 0, ErrInvalidArgument
	}
	compiled, err := edit.Compile(table)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	mu.Lock()
	defer mu.Unlock()
	tr, err := getTrack(id)
	if err != nil {
		return 0, 0, 0, err
	}
	latest := len(tr.vers) - 1
	if base > latest {
		return 0, 0, 0, ErrVersionNotFound
	}
	if base != latest {
		return 0, 0, 0, ErrConflict
	}
	r := edit.Retime(compiled, tr.vers[latest].cues, tr.dmin)
	tr.vers = append(tr.vers, entry{kind: 1, cues: r.Cues, table: compiled})
	return latest + 1, r.Splits, r.Dropped, nil
}

// CommitRebased 允许 base 早于最新版本；base 之后直到最新的每个版本都必须
// 是 Retime 产生的，否则 ErrConflict。cues 先在 base 时间线上校验，再依次
// 经过这些剪辑表重定时（每步都丢弃），结果成为普通提交版本。
func CommitRebased(id string, base int, cues []cue.Cue) (int, error) {
	if id == "" || base < 0 {
		return 0, ErrInvalidArgument
	}
	mu.Lock()
	defer mu.Unlock()
	tr, err := getTrack(id)
	if err != nil {
		return 0, err
	}
	latest := len(tr.vers) - 1
	if base > latest {
		return 0, ErrVersionNotFound
	}
	if base != latest {
		for v := base + 1; v <= latest; v++ {
			if tr.vers[v].kind != 1 {
				return 0, ErrConflict
			}
		}
	}
	if err := cue.Validate(cues, tr.dmin); err != nil {
		return 0, wrapCue(err)
	}
	cur := cloneCues(cues)
	for v := base + 1; v <= latest; v++ {
		cur = edit.Retime(tr.vers[v].table, cur, tr.dmin).Cues
	}
	tr.vers = append(tr.vers, entry{kind: 0, cues: cur})
	return latest + 1, nil
}

// Get 取任一版本；越界（含负数）报 ErrVersionNotFound。
func Get(id string, v int) (Version, error) {
	if id == "" {
		return Version{}, ErrInvalidArgument
	}
	mu.Lock()
	defer mu.Unlock()
	tr, err := getTrack(id)
	if err != nil {
		return Version{}, err
	}
	if v < 0 || v > len(tr.vers)-1 {
		return Version{}, ErrVersionNotFound
	}
	return Version{Number: v, Cues: cloneCues(tr.vers[v].cues)}, nil
}

func cloneCues(cs []cue.Cue) []cue.Cue {
	if len(cs) == 0 {
		return []cue.Cue{}
	}
	out := make([]cue.Cue, len(cs))
	copy(out, cs)
	return out
}

// wrapCue 保留 *cue.InvalidError（errors.As 可取），其余参数非法包装为
// ErrInvalidArgument。
func wrapCue(err error) error {
	var inv *cue.InvalidError
	if errors.As(err, &inv) {
		return err
	}
	return fmt.Errorf("%w: %v", ErrInvalidArgument, err)
}
