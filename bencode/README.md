# bencode 流式增量解码器

把任意切分的字节流还原为值树；规范性被破坏时按**首个违规字节**精确拒绝。
解出的值序列、累计消费字节数与错误偏移/原因与切分方式无关（含逐字节喂入）。

## 值模型

| bencode | Go 类型 |
| --- | --- |
| 整数 | `int64` |
| 字节串 | `[]byte` |
| 列表 | `[]any` |
| 字典 | `bencode.Dict`（`[]Pair`，保持键序） |

## 规范性规则

- **整数** `i<十进制>e`：允许前导负号；不允许加号与前导零；`0` 合法，`-0` 不合法；
  取值范围为 int64（`-9223372036854775808` 合法，`9223372036854775808` 溢出）。
- **字节串** `<长度>:<内容>`：长度为无前导零的非负十进制（`0:` 合法），
  且不得超过上限 `MaxString`（默认 `DefaultMaxString` = 16 MiB）。
- **列表** `l...e`、**字典** `d...e`：嵌套深度上限为 `D`（默认 `DefaultMaxDepth` = 128），
  顶层列表/字典深度为 1，第 D+1 层的开括号字节即违规点。
- **字典键**必须是字节串，并按原始字节序严格升序：较短的前缀排在前，空串最小，
  相等即重复键。
- 流可连续包含多个顶层值，每个值一完成即由 `Feed` 返回。

## 错误偏移约定

错误为 `*bencode.Error`，`Offset` 是首个违规字节在整个流中的绝对偏移（从 0 开始），
原因可用 `errors.Is` 区分：

| 原因（sentinel） | 违规点 |
| --- | --- |
| `ErrIllegalFirstByte` | 该字节本身 |
| `ErrIntLeadingZero` | 紧随前导 `0` 之后的那个数字字节 |
| `ErrNegativeZero` | 负号后的 `0` |
| `ErrIntOverflow` | 使数值越界的那个数字字节 |
| `ErrLenLeadingZero` | 紧随前导 `0` 之后的那个数字字节 |
| `ErrLenTooLarge` | 使长度越界的那个数字字节 |
| `ErrDepthExceeded` | 第 D+1 层的开括号 |
| `ErrKeyNotString` | 该键的首字节 |
| `ErrKeyOrder` / `ErrDuplicateKey` | 该键长度前缀的首字节 |
| `ErrSyntax` | 整数内非数字非 `e` 的字节（含 `ie`、`i-e` 的 `e`）、长度后非数字非冒号的字节 |

## 粘滞失败态

一旦出错：本次 `Feed` 仍交付出错点之前已完整的值；此后所有 `Feed` 返回
`ErrPoisoned` 且不改变任何状态与计数；原始错误可由 `Err()` 查询。

## API

```go
dec := bencode.NewDecoder()                      // 默认限制
dec = bencode.NewDecoderWithLimits(maxStr, maxDepth)
vals, err := dec.Feed(chunk)                     // 本次新完成的顶层值
n := dec.Consumed()                              // 累计消费字节数
m := dec.Buffered()                              // 尚未完成的缓冲字节数
origErr := dec.Err()                             // 粘滞失败态的原始错误
```

`Feed` 与查询均可并发调用，结果等价于某个串行顺序（内部互斥锁保护）。

## 本地验证

```bash
# 全量测试（含切分一致性穷举、朴素整体解码器对照、随机变异用例）
go test ./bencode/

# 竞态检测 + 详细日志（日志打印输入、输出与判定依据）
go test -race -v ./bencode/

# 单个用例
go test -run 'TestDictKeyRules/重复键' -v ./bencode/
```
