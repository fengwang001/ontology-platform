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

## 流式分词器（`tokenizer` 包）

`tokenizer` 提供带字符过滤的流式分词器：UTF-8 文本流以任意切分的字节块
`Feed` 进入，经折叠与删除后切成小写字母数字词元，并把词元起止映射回原文字节偏移。
词元序列、位置序号与偏移不随输入切分点变化；`Feed`/`Close` 并发安全，
结果等价于某个串行顺序。

### 字符过滤表

每个原文字符先过滤成零或多个输出字节：

| 原文字符 | 输出 |
| --- | --- |
| U+0300–U+036F 组合记号 | 空（删除，既不是词元字节也不是分隔符，不打断词元） |
| `ß` | `ss` |
| `æ` / `Æ` | `ae` |
| `œ` / `Œ` | `oe` |
| U+FB01 | `fi` |
| U+FB02 | `fl` |
| ASCII 大写 `A`–`Z` | 对应小写 |
| 其余字符 | 原样输出其 UTF-8 字节 |

输出字节中 `[a-z0-9]` 是词元字节，其余（含未映射非 ASCII 字符的各字节）是分隔符；
词元是被分隔符隔开的极大词元字节连续段。

### 偏移映射与位置序号

- `Start`：词元首个词元字节所来自的原文字符的起始字节偏移（整个流从 0 计）。
- `End`（开区间）：末个词元字节所来自字符的结束偏移，再向后吞并紧随其后的
  全部连续被删除字符的结束偏移。
- 位置序号按出现顺序从 0 递增；输出字节数超过 64 的词元不报告，但仍占一个
  位置序号（序号出现缺口）并计入 `Dropped`，恰为 64 的保留。
- 词元在其后第一个分隔符字符被完整接收的那次 `Feed` 中返回；流末尾的词元在
  `Close` 中返回。`Close` 统计已报告词元数（不含 `Dropped`）、`Dropped`
  与消耗字节数。

### 拒绝语义

拒绝原因可用 `errors.Is` 区分，被拒绝的操作不改变任何状态与统计：

- `Feed`：已关闭报 `ErrClosed`；块与暂存尾部拼接后含已能确定非法的 UTF-8
  （过长编码、代理项、超过 U+10FFFF 等）报 `ErrInvalidEncoding`，整块拒绝；
  合法但尚不完整的尾部不算非法，暂存待后续块补齐。
- `Close`：重复 `Close` 报 `ErrClosed`；仍有暂存的不完整尾部报
  `ErrTruncated`，此时不关闭，可继续 `Feed` 补齐后再 `Close`。

### 本地验证

```bash
# 全部测试（含 2000 组随机输入与朴素一次性实现对拍，
# -v 日志打印每组的输入、输出与判定依据）
go test ./tokenizer/
go test -race -v ./tokenizer/

# 指定用例
go test -run TestTokenLengthLimit ./tokenizer/
```
