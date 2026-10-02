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

## bencode 流式增量解码器（`./bencode`）

`bencode.Decoder` 把任意切分的字节流还原为值树：通过 `Feed([]byte)`
增量喂入，每个顶层值一完成即随本次 `Feed` 返回；`Consumed()` 与
`Buffered()` 查询累计已消费字节数与尚未完成的缓冲字节数。所有方法
可并发调用，结果等价于某个串行顺序。`DecodeAll` 提供整体解码的便捷
入口（尾部不完整时返回 `ErrTruncated`）。

### 规范性规则

- 整数：`i` + 十进制 + `e`。允许前导负号，不允许加号与前导零；
  `i0e` 合法，`i-0e`（负零）非法；取值范围为 int64
  （`-9223372036854775808` 合法，`9223372036854775808` 溢出）。
- 字节串：长度（无前导零的非负十进制，`0:` 合法）+ `:` + 内容，
  长度不得超过 `Options.MaxString`（默认 16 MiB）。
- 列表 `l...e`、字典 `d...e`；嵌套深度上限为 `Options.MaxDepth`
  （默认 128），顶层列表/字典深度为 1。
- 字典键必须是字节串，并按原始字节序严格升序：较短的前缀排在前，
  空串最小，相等即重复键。
- 流可连续包含多个顶层值。

### 错误与偏移约定

解码在首个违规字节处拒绝，返回 `*bencode.Error`（含 `Reason` 与绝对
偏移 `Offset`），可用 `errors.Is` 区分原因：

| 原因 | 违规点（偏移所指字节） |
| --- | --- |
| `ErrBadLeadingByte` | 非法首字节本身 |
| `ErrIntLeadingZero` | 紧随前导 `0` 之后的那个数字 |
| `ErrNegativeZero` | 负号后的 `0` |
| `ErrIntOverflow` | 使数值越界的那个数字 |
| `ErrLenLeadingZero` | 紧随前导 `0` 之后的那个数字 |
| `ErrStringTooLong` | 使长度超过上限的那个数字 |
| `ErrDepthExceeded` | 第 D+1 层的开括号 |
| `ErrKeyNotString` | 该键的首字节 |
| `ErrKeyOutOfOrder` | 乱序键长度前缀的首字节 |
| `ErrDuplicateKey` | 重复键长度前缀的首字节 |
| `ErrSyntax` | 整数/长度前缀中的非数字字节（含 `ie`、`i-e` 的 `e`） |

出错的那次 `Feed` 仍交付出错点之前已完整的值；此后解码器进入粘滞
失败态：所有 `Feed` 返回 `ErrPoisoned` 且不改变任何状态与计数，
原始错误可通过 `Err()` 查询。

无论输入如何切分（含逐字节），交付的值序列、累计消费字节数、错误
偏移与原因都相同。

### 本地验证

```bash
# 全量测试（含所有切分点遍历与朴素整体解码器对照）
go test ./bencode

# 竞态检测 + 详细日志（打印输入、输出与判定依据）
go test -race -v ./bencode
```
