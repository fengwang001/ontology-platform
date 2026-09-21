package walstore

import (
	"io"
	"os"
	"path/filepath"
)

// recoverLog 打开并校验 WAL，重放全部合法帧得到当前状态。
// 它保证返回时：
//   - 文件已被截断到最后一条合法帧的末尾，后续写入不会接在垃圾后；
//   - 文件偏移位于末尾（便于追加）；
//   - 任何半截/损坏尾部都被静默丢弃，绝不因此报错。
func recoverLog(dir string) (*os.File, map[string]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, err
	}
	path := filepath.Join(dir, walName)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, nil, err
	}

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		return nil, nil, err
	}
	frames, valid, err := readFrames(f)
	if err != nil && err != io.EOF {
		f.Close()
		return nil, nil, err
	}

	state := replay(frames)

	// 截掉尾部垃圾（含半截记录），保证追加边界干净。
	if err := f.Truncate(int64(valid)); err != nil {
		f.Close()
		return nil, nil, err
	}
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		f.Close()
		return nil, nil, err
	}
	return f, state, nil
}

// replay 按日志顺序应用帧：checkpoint 用快照整体替换状态，
// 其后的 batch 依次合并，因此提交顺序天然保留。
func replay(frames []frame) map[string]string {
	var state map[string]string
	for _, fr := range frames {
		switch fr.typ {
		case recCheckpoint:
			if snap, ok := decodeBatch(fr.payload); ok {
				state = snap // 整体替换，丢弃检查点之前的历史
			}
		case recBatch:
			if batch, ok := decodeBatch(fr.payload); ok {
				if state == nil {
					state = make(map[string]string)
				}
				for k, v := range batch {
					state[k] = v
				}
			}
		}
	}
	if state == nil {
		state = make(map[string]string)
	}
	return state
}
