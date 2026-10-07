package snapshot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// ExportRequest 描述一次分块快照导出。
type ExportRequest struct {
	// Chunks 按类型分组的记录；外层 key 为对象类型。
	Chunks map[string][][]Record
}

// Export 把记录按类型拆分为多个块并落盘到 dir。
// 导出结果一经落盘不再被本进程修改；后续所有 Load/Aggregate
// 均为只读操作，同一输入必然得到同一结论。
func Export(dir string, req ExportRequest, logger DecisionLogger) error {
	if logger == nil {
		logger = nopLogger{}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	types := make([]string, 0, len(req.Chunks))
	for typ := range req.Chunks {
		types = append(types, typ)
	}
	sort.Strings(types)

	chunkFiles := make(map[string][]string, len(types))
	for _, typ := range types {
		groups := req.Chunks[typ]
		files := make([]string, 0, len(groups))
		for idx, records := range groups {
			ref := ChunkRef{Type: typ, Chunk: idx}
			checksum, err := computeChecksum(records)
			if err != nil {
				return fmt.Errorf("snapshot: checksum %v: %w", ref, err)
			}
			env := Envelope{
				Header: Header{
					Type:          typ,
					ChunkIndex:    idx,
					DeclaredCount: len(records),
					ChecksumAlgo:  checksumAlgoSHA256,
				},
				Records:  records,
				Checksum: checksum,
			}
			name := chunkFileName(typ, idx)
			path := filepath.Join(dir, name)
			raw, err := json.MarshalIndent(env, "", "  ")
			if err != nil {
				return err
			}
			if err := os.WriteFile(path, raw, 0o644); err != nil {
				return err
			}
			files = append(files, name)
			logger.Log(Decision{
				Stage:  "export_chunk",
				Chunk:  &ref,
				Input:  fmt.Sprintf("type=%s chunk=%d records=%d", typ, idx, len(records)),
				Output: "written",
				Basis:  "sha256(records) 写入块头声明条数与校验和",
			})
		}
		chunkFiles[typ] = files
	}

	manifestRaw, err := json.MarshalIndent(manifestFor(types, chunkFiles), "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifestRaw, 0o644); err != nil {
		return err
	}
	logger.Log(Decision{
		Stage:  "export_manifest",
		Input:  fmt.Sprintf("types=%v", types),
		Output: "written",
		Basis:  "manifest 仅声明覆盖范围，不参与块级完整性判定",
	})
	return nil
}

func chunkFileName(typ string, idx int) string {
	return fmt.Sprintf("chunk-%s-%d.json", sanitize(typ), idx)
}

func sanitize(typ string) string {
	out := make([]rune, 0, len(typ))
	for _, r := range typ {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}

type nopLogger struct{}

func (nopLogger) Log(Decision) {}
