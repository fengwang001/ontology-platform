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

## 流式分词器（`ontology` 包）

`ontology.NewTokenizer()` 提供一个带字符过滤的流式 UTF-8 分词器：

```go
tz := ontology.NewTokenizer()
toks1, err := tz.Feed(chunk) // 可任意切分字节块，可跨 UTF-8 字符边界
toks2, err := tz.Close()     // 刷新流末尾词元；成功后才真正关闭
stats := tz.Stats()          // {Tokens, Dropped, Consumed}
```

### 字符过滤表

每个原文字符（按 Go 的 UTF-8 定义逐字符解码）先过滤成零或多个输出字节：

| 原文字符 | 输出 | 说明 |
| --- | --- | --- |
| `U+0300`–`U+036F`（组合记号） | 空 | 删除：既不是词元字节也不是分隔符，不打断词元 |
| `ß` | `ss` | 折叠 |
| `æ` / `Æ` | `ae` | 折叠 |
| `œ` / `Œ` | `oe` | 折叠 |
| `U+FB01`（ﬁ，3 字节 UTF-8） | `fi` | 折叠 |
| `U+FB02`（ﬂ） | `fl` | 折叠 |
| ASCII 大写 `A-Z` | 对应小写 | 折叠 |
| 其余字符 | 其 UTF-8 字节原样输出 | 非 ASCII 字符的各字节均落在 `[a-z0-9]` 之外 |

输出字节中属于 `[a-z0-9]` 的为**词元字节**，其余（含未映射非 ASCII 字符的各字节）为**分隔符**。
词元是被分隔符隔开的极大词元字节连续段。

### 偏移映射

- 偏移按整个流从 0 起的原文字节计。
- `Start`：词元首个词元字节所来自的原文字符的起始字节偏移。
- `End`（开区间）：词元末个词元字节所来自字符的结束偏移，再向后吞并紧随其后的全部连续被删除字符的结束偏移。
- 跨块到达的不完整 UTF-8 尾部暂存，待后续块补齐；暂存字节在字符完整前不计入 `Consumed`。

### 位置序号、长度与词元返回时机

- 词元按出现顺序获得序号 `0, 1, 2, …`。
- 输出字节数 **恰为 64** 的词元保留；**超过 64** 的词元不报告但仍占用一个序号（序号出现缺口），并计入 `Dropped`。
- 词元在其后第一个分隔符字符被完整接收的那次 `Feed` 中返回（同次 `Feed` 内按序号升序）；流末尾词元在 `Close` 中返回。
- `Close` 成功后，`Stats` 给出已报告词元数（不含 `Dropped`）、`Dropped` 与已消耗字节数。

### 拒绝原因（互不混淆，拒绝不改变任何状态与统计）

- `Feed`：`ErrClosed`（已关闭）优先；其次 `ErrInvalidUTF8`——块与暂存尾部拼接后含已能确定不是任何合法 UTF-8 前缀的序列（含过长编码、代理项、超过 `U+10FFFF`），整块拒绝；合法但尚不完整的尾部不算非法。
- `Close`：`ErrClosed`（第二次关闭）优先；其次 `ErrTruncated`（仍有不完整尾部，此时**不关闭**，可继续 `Feed` 补齐后再 `Close`）。

`Feed`/`Close`/`Stats` 由互斥串行化，结果等价于某个串行调用顺序；同一字节流无论如何切分，返回词元（文本、`Start`/`End`、`Pos`）拼接后逐项相同；返回切片与字符串不别名内部缓冲与入参缓冲。

### 本地验证

```bash
# 全量测试（若 GOCACHE 默认目录只读，可指定可写缓存目录）
GOCACHE=/tmp/gocache go test -race -count=1 ./ontology/

# 2000 组随机输入对拍（朴素整段处理 vs 四种喂入切分），打印输入/输出/判定依据
GOCACHE=/tmp/gocache go test -run TestRandomDifferential2000 -v ./ontology/

go vet ./...
gofmt -l .
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
