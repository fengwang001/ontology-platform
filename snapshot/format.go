package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// manifestFile 是导出根目录下 manifest.json 的结构。
// 它本身只负责“本次导出覆盖哪些类型、各类型块文件在哪”，
// 不参与块级完整性判定：每个块的可信性完全由其自身
// 声明的 checksum 与记录内容决定，与其它块无关。
type manifestFile struct {
	Types  []string            `json:"types"`
	Chunks map[string][]string `json:"chunks"`
}

const checksumAlgoSHA256 = "sha256"

// payloadBytes 是校验和覆盖的范围：仅记录内容本身。
// 块头（含声明条数）与校验字段都不纳入校验范围，因此
// “条数不符”与“校验失败”是两类可区分、互斥的问题。
func payloadBytes(records []Record) ([]byte, error) {
	return json.Marshal(records)
}

func computeChecksum(records []Record) (string, error) {
	raw, err := payloadBytes(records)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func manifestFor(types []string, chunkFiles map[string][]string) manifestFile {
	sorted := append([]string(nil), types...)
	sort.Strings(sorted)
	return manifestFile{Types: sorted, Chunks: chunkFiles}
}
