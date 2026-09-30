# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## diffsync：基于块签名的差分同步器

`diffsync` 包实现 rsync 风格的三方流程：接收端对旧文件生成块签名 →
发送端据此为新文件生成增量补丁 → 接收端原子地应用补丁。

### 两级校验分工

- **弱校验（字节和，uint32）**：对每个固定大小块（末块可短）计算，
  只用于快速筛选候选块。它极易碰撞，因此**绝不**单独作为复用依据。
- **强校验（SHA-256）**：弱校验命中且块长度相等（末块只与等长片段匹配）
  后，必须再经 SHA-256 确认内容一致才发出「复用第 i 块」指令，
  否则按字面字节输出。强校验次数 ≤ 弱匹配次数，由 `Stats` 对外报告。

### 补丁格式（大端序）

```
magic      [8]byte   "BDSYNC01"
blockSize  uint32
oldLen     uint64
oldStrong  [32]byte  旧文件整体 SHA-256
newLen     uint64
newStrong  [32]byte  新文件整体 SHA-256
insnCount  uint32
insns      每条指令：op uint8
             op=0 复用：blockIndex uint32
             op=1 字面：dataLen uint32 + data
```

相邻字面字节在生成时合并；同一输入反复生成的补丁逐字节相同。

### 原子应用规则

`Apply` 是纯函数：全部校验通过后才返回结果，任何失败都不产生写入。
`ApplyFileAtomic` 先在内存中重建并校验，再写入同目录临时文件、
fsync 后 rename 覆盖目标。以下情况整体拒绝且目标逐字节不变，
原因可用 `errors.Is` 区分：

- `ErrInvalidBlockSize`：块大小非正
- `ErrBadMagic` / `ErrPatchTruncated`：补丁非法或被截断
- `ErrOldChecksumMismatch`：补丁声明的旧文件校验与目标端不符
- `ErrInstructionOutOfRange`：指令操作码未知、块索引越界或有多余字节
- `ErrResultChecksumMismatch`：重建结果与补丁声明的整体校验不符

签名生成后不可变，生成与应用均可并发调用（`go test -race` 验证）。

### 本地验证

```bash
go test -race -v ./diffsync   # 日志打印输入、输出与判定依据
```

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 拉取依赖
go mod tidy

# 直接运行
go run ./cmd/server

# 编译后运行
go build -o bin/server ./cmd/server
./bin/server
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./ontology
go test -run TestObjectType ./ontology

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
