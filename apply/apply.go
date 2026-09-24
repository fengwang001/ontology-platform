// Package apply 持锁执行线性步骤序列，写 CRC 保护的撤销日志，并在中途失败时自动回滚。
package apply

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"os"

	"ontology/cycle"
	"ontology/name"
	"ontology/plan"
)

// Magic 是日志文件头；HeaderLen 为其长度。
const (
	Magic     = "RNML1\n"
	HeaderLen = 6
)

var crcTable = crc32.MakeTable(crc32.IEEE)

// ErrInjected 是故障注入返回的哨兵错误。
var ErrInjected = errors.New("injected failure")

// Executor 执行一批重命名。
type Executor struct {
	Space *name.Space
	Log   string
	// FailAt>0 时，在第 FailAt 步（1 基）执行前注入失败并自动回滚。
	FailAt int
}

// EncodeFrame 返回一步的日志帧：oldLen newLen old new crc32。
func EncodeFrame(old, nw string) []byte {
	payload := append([]byte(old), []byte(nw)...)
	buf := make([]byte, 8+len(old)+len(nw)+4)
	binary.LittleEndian.PutUint32(buf[0:4], uint32(len(old)))
	binary.LittleEndian.PutUint32(buf[4:8], uint32(len(nw)))
	copy(buf[8:], old)
	copy(buf[8+len(old):], nw)
	sum := crc32.Checksum(append(append([]byte(nil), buf[:8]...), payload...), crcTable)
	binary.LittleEndian.PutUint32(buf[len(buf)-4:], sum)
	return buf
}

// Prepare 做冲突检测并展开环，返回最终线性步骤。
func Prepare(sp *name.Space, reqs []plan.Req) ([]plan.Step, error) {
	existing := sp.Snapshot()
	p, err := plan.Build(existing, reqs)
	if err != nil {
		return nil, err
	}
	ex, err := cycle.Expand(p, existing, reqs)
	if err != nil {
		return nil, err
	}
	return cycle.LinearSteps(p, ex), nil
}

// Run 检测、展开并持锁执行；失败时逆序回滚，命名空间恢复执行前状态。
func (x *Executor) Run(reqs []plan.Req) (steps []plan.Step, err error) {
	steps, err = Prepare(x.Space, reqs)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(x.Log, os.O_CREATE|os.O_TRUNC|os.O_WRONLY|os.O_SYNC, 0o600)
	if err != nil {
		return nil, err
	}
	if _, err := f.WriteString(Magic); err != nil {
		f.Close()
		return nil, err
	}
	x.Space.Lock()
	executed := 0
	defer func() {
		if err != nil {
			for i := executed - 1; i >= 0; i-- {
				x.Space.Move(steps[i].New, steps[i].Old)
			}
		}
		x.Space.Unlock()
		f.Close()
	}()
	for _, st := range steps {
		if x.FailAt > 0 && executed+1 == x.FailAt {
			err = fmt.Errorf("%w at step %d", ErrInjected, executed+1)
			return steps, err
		}
		if !x.Space.Move(st.Old, st.New) {
			err = fmt.Errorf("rename failed without overwrite: %v", st)
			return steps, err
		}
		if _, werr := f.Write(EncodeFrame(st.Old, st.New)); werr != nil {
			err = werr
			return steps, err
		}
		executed++
	}
	return steps, nil
}
