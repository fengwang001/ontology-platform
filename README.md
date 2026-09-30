# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

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

## 块签名差分同步器（`ontology/diffsync`）

`diffsync` 包实现 rsync 风格的三方流程：接收端对旧文件生成块签名，
发送端据此为新文件生成增量补丁，接收端原子地应用补丁，结果与新文件
逐字节相同。

### 两级校验分工

- **弱校验（32 位滚动校验和）**：`a` 为字节和、`b` 为位置加权和，均取
  mod 2^16。计算廉价、可 O(1) 滚动，用于在新文件的**每个偏移**上快速
  筛出候选旧块；允许碰撞，碰撞不构成正确性风险。
- **强校验（SHA-256）**：仅当弱校验命中且**块长相等**时才计算并比对，
  用于最终确认复用。末块（可能短于块大小）只与等长片段互相匹配。
  强校验次数不超过弱匹配次数，生成端通过 `Delta.Stats`
  （`WeakMatches` / `StrongChecks`）对外报告。

弱校验相同而内容不同的块不会被复用（强校验拒绝），对应字节以字面量
输出；相邻字面量自动合并为一条指令。

### 补丁格式（大端序，`MarshalPatch` 输出）

```
magic        4 字节   "ODP1"
blockSize    uint32
oldLength    uint64
oldChecksum  32 字节  旧文件整体 SHA-256
newLength    uint64
newChecksum  32 字节  期望结果整体 SHA-256
instrCount   uint32
instructions 每条为：
  复用: 0x01, blockIndex uint32        —— 复用旧文件第 i 块
  字面: 0x02, length uint32, data      —— 原样输出 length 个字节
```

同一输入反复生成的补丁逐字节相同；签名生成后不可变，可被多个发送端
并发使用，生成与应用均可并发调用。

### 原子应用规则

`ApplyPatch` 先在内存中完成全部校验再产出结果；`ApplyPatchToFile`
通过「同目录临时文件 + fsync + rename」落盘。以下任一失败都整体拒绝、
目标端逐字节不变，且原因可用 `errors.Is` 区分：

- `ErrInvalidBlockSize`：块大小非正（含补丁声明为 0）。
- `ErrOldChecksumMismatch`：补丁声明的旧文件长度/校验与本地不符。
- `ErrTruncatedPatch`：补丁在任意位置被截断。
- `ErrMalformedPatch`：魔数错误、未知指令或尾部多余字节。
- `ErrInstructionOutOfRange`：复用指令引用了不存在的旧块。
- `ErrResultChecksumMismatch`：重建结果的长度/整体校验与声明不符。

### 本地验证

```bash
# 单元测试（含弱校验碰撞、逐字节截断、并发与原子性用例，日志打印
# 输入、输出与判定依据）
go test ./diffsync -v

# 竞态检测
go test ./diffsync -race
```
