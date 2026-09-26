# NOTES

## 推导（三帧 hi / a\nb / x\y）
| 负载 | 特殊字节 | 转义后帧字节(hex,含结尾 0A) |
|---|---|---|
| `hi`   | 无              | `68 69 0A` |
| `a\nb` | 1 个 0A（下标1） | `61 5C 6E 62 0A` |
| `x\y`  | 1 个 5C（下标1） | `78 5C 5C 79 0A` |

整条流：`68 69 0A 61 5C 6E 62 0A 78 5C 5C 79 0A`

- (甲) 正确：第2帧 3 字节 `61 0A 62`（`a`+换行+`b`）。只按裸 0A 切分不反转义：4 字节 `61 5C 6E 62`（`a`、`\`、`n`、`b`）。
- (乙) 帧含 `5C 78`：正确 → `esc.ErrIllegalEscape`，整流拒绝、返回 nil；naive 把 `\x` 当两个普通字节，得 2 字节 `5C 78`。
- (丙) `61 62 5C` 后紧跟分隔符 0A：该 `\` 是**悬空转义**（帧边界前的转义字节）。正确 → `esc.ErrDanglingEscape`；naive 当普通字节得 `ab\`（或丢弃得 `ab`），均属脏数据。
- 三个哨兵互不相同：`esc.ErrIllegalEscape`、`esc.ErrDanglingEscape`、`frame.ErrMissingTerminator`。

## 不变量在何处由谁保证
1. 往返一致：`frame.Decode` 单趟状态机逐字节 append、`frame.Encode` 逐帧转义加 0A；测试 `TestRoundTrip`（含空帧/特殊字节/随机）。
2. 转义可逆：`esc.Escape`/`esc.Unescape` 互为逆的两张转义表；测试 `TestEscapeVectors`、`TestRoundTrip`。
3. 与朴素参照字节一致：`frame.Encode`；测试 `TestEncodeMatchesNaive`（独立教科书实现 + 随机向量）。
4. 失败不留痕：`Decode` 任何错误路径 `return nil, err`（丢弃已攒帧）；`Reader` 仅成功时提交 `pos`；测试 `TestDecodeFailures`、`TestReaderCursorNoAdvance`。
- 单趟线性：`frame.scanStats`（包级 `lastDecode`）的非导出原子字段 `examined`/`reread`；测试 `TestSinglePassCounter` 断言 examined 恰为流长度、reread 恒为 0。
- 并发：编解码无跨调用状态（计数器为原子量）；测试 `TestConcurrentEncodeDecode`（-race）。自检 `api.SelfCheck` 由 `TestSelfCheck` 钉住。
