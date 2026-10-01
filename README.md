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

## fragmentlog：块对齐的记录分片日志

`./fragmentlog` 把任意长度的记录切成不跨块的片段，写入固定大小（B 字节）的块，
并能从字节流中读回记录，在各类损坏下可区分地报错并按固定规则恢复。

### 片段格式

每个片段 = 7 字节头 + 数据，片段绝不跨块：

- `[0:4]` 小端 crc32（IEEE），对「类型字节 + 数据」计算
- `[4:6]` 小端 uint16 数据长度
- `[6]` 类型：1 完整、2 首片、3 中片、4 末片

### 块对齐规则（Writer）

- 设当前块剩余 `rem`：`rem < 7` 时补 `rem` 个零字节，片段从下一个块开始。
- 每个片段数据长度取 `min(rem-7, 尚未写入的字节数)`。
- 首个片段写完全部字节为「完整」，未写完为「首片」；非首片未写完为「中片」、
  写完为「末片」。
- `rem == 7` 且记录尚有字节时写一个零长数据的「首片」；空记录在 `rem >= 7`
  时写一个零长「完整」片段。
- `Append` 返回该记录首个片段头的起始偏移（补零时为新块起点）。
- 构造时 `B <= 7` 或 `B > 65535` 分别返回 `ErrBlockSizeTooSmall` /
  `ErrBlockSizeTooLarge`；记录超过 `MaxRecord`（1048576）时 `Append`
  整体拒绝并返回 `ErrRecordTooLarge`，已写字节与偏移不变。

### 读侧错误与恢复（Reader）

四类可区分的错误（`errors.Is` 判定，错误值为 `*CorruptError`，携带出错片段头偏移）：

- `ErrLength`：数据长度越出所在块（仅凭头部即判，先于任何数据读取）
- `ErrChecksum`：校验不符
- `ErrSequence`：类型序列非法（类型值不在 1..4、无首片的中片/末片、
  记录未收尾又遇首片/完整）
- `ErrTruncated`：流在片段中途或记录未收尾处结束

判定顺序：头不足 7 字节或长度越块 → 数据字节不足（截断）→ 校验 → 类型序列。
块内剩余不足 7 字节视为填充跳过。出错时丢弃正在组装的记录（含触发错误的片段），
跳到出错片段头所在块的下一个块边界继续；流尾截断报错后下一次 `Next` 返回
`io.EOF`；干净读到末尾直接返回 `io.EOF`。

### 并发

`Writer.Append`、`Reader.Next` 与内存型 `Log`（`Append` + `Reader`）均可并发调用，
结果等价于某个串行顺序；相同追加序列产生逐字节相同的输出与偏移。

### 本地验证

```bash
# 全部测试（含逐字节翻转、逐位置截断与朴素实现对拍）
go test ./fragmentlog/

# 查看每条用例的输入、输出与判定依据日志
go test -v ./fragmentlog/

# 竞态检测
go test -race ./fragmentlog/
```
