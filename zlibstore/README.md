# zlibstore：仅存储块的 zlib 流式容器

`zlibstore` 实现只使用 DEFLATE **存储块**（stored block，BTYPE=00，不做任何压缩）
的 zlib（RFC 1950）容器流式编码器 `Encoder` 与解码器 `Decoder`。
所有字节布局都是确定的，可逐字节复现，并且解码结果与输入的切分方式无关。

## 容器格式

```
┌──────────┬─────────────────────────────┬──────────────────┐
│ 2B 头部  │ 若干存储块（≥1）            │ 4B Adler-32 尾部 │
└──────────┴─────────────────────────────┴──────────────────┘
```

- **头部**：固定 `78 01`。CM=8（deflate）、CINFO=0（32 字节窗口）、
  FDICT=0、FLEVEL=0，FCHECK 使头部按大端解释的 16 位整数被 31 整除。
- **存储块**（RFC 1951 §3.2.4）：
  - 1 字节块头：bit0 为 BFINAL，bit1–2 为 BTYPE（存储块为 `00`），高 5 位必须为 0。
  - 2 字节小端 `LEN`：块数据字节数（0–65535）。
  - 2 字节小端 `NLEN`：必须等于 `LEN` 的按位取反低 16 位。
  - `LEN` 字节原始数据。
- **尾部**：4 字节大端 Adler-32。

空输入的完整输出为：

```
78 01 01 00 00 FF FF 00 00 00 01
```

## 块切分规则

- `Write` 的数据进入内部缓冲（容量 65535），每攒满 65535 字节立即输出一个
  **BFINAL=0** 的非终块。
- `Close` 把剩余数据（**允许 0 字节**）作为唯一的 **BFINAL=1** 终块输出，
  然后追加 Adler-32 尾部。
- 因此长度 65535 的输入是「非终满块 + 0 字节终块」，
  长度 65536 是「非终满块 + 1 字节终块」。
- 输出只取决于写入的全部字节：任意 `Write` 切分（含逐字节）产生逐字节相同的结果；
  相同输入重放也完全一致。
- `Close` 之后再 `Write` 或再 `Close` 返回 `ErrClosed`，被拒绝的调用不改变状态与输出。

## Adler-32 定义

```
a = 1, b = 0
对每个字节 byte：
    a = (a + byte) mod 65521
    b = (b + a)    mod 65521
校验和 = (b << 16) | a
```

已知向量：空输入 `0x00000001`，字符串 `"Wikipedia"` 为 `0x11E60398`。

## 解码语义与错误

- 数据块内容**边收边交付**：`Write` 返回本次新解出的数据（不缓存整块），
  `Delivered()` 查询已交付字节总数。
- 解码结果与输入切分无关：任意切分（含逐字节）交付的数据、首个错误与
  `Delivered` 计数完全相同。
- 一旦出错进入**粘滞失败态**：此后 `Write`/`Close` 返回 `ErrPoisoned`
  且不改变任何状态；`Delivered` 仍可查询。
- `Close` 时若未读完整头部/块/4 字节尾部，返回可区分的 `ErrTruncated`（同样粘滞）。

错误按检测顺序只报第一个，哨兵错误均可用 `errors.Is` 区分：

| 阶段 | 错误 | 触发条件 |
| --- | --- | --- |
| 头部 | `ErrBadMethod` | CMF 低 4 位压缩方法 ≠ 8 |
| 头部 | `ErrBadWindow` | CMF 高 4 位 CINFO > 7 |
| 头部 | `ErrBadCheckValue` | 头部 16 位大端值不被 31 整除 |
| 头部 | `ErrDictionaryPresent` | FLG 的 FDICT（0x20）被置位 |
| 块头 | `ErrBlockHeaderReserved` | 块头高 5 位非 0 |
| 块头 | `ErrBlockFixedHuffman` | BTYPE=01（不支持） |
| 块头 | `ErrBlockDynamicHuffman` | BTYPE=10（不支持） |
| 块头 | `ErrBlockReservedType` | BTYPE=11（保留） |
| 块元 | `ErrBadNLEN` | NLEN ≠ ~LEN（非终块 LEN 允许为 0） |
| 尾部 | `ErrTrailingBytes` | 4 字节尾部之后仍有字节 |
| 尾部 | `ErrChecksum` | Adler-32 与已交付数据不符 |
| 收尾 | `ErrTruncated` | Close 时流不完整 |
| 粘滞 | `ErrPoisoned` | 首次错误之后的一切调用 |

`Encoder`/`Decoder` 的所有方法都可用多 goroutine 并发调用，
效果等价于某个串行顺序（内部互斥保护）。

## 本地验证

```bash
# 单元测试（日志打印输入、输出字节与判定依据）
go test -v ./zlibstore

# 竞态检测
go test -race -count=1 ./zlibstore

# 全量测试、格式化与静态检查
go test ./...
gofmt -l .
go vet ./...
```

测试覆盖：空输入精确字节、65535/65536（及 ±1、131070–131072）块切分、
全 0xFF 数据的 Adler 取模、Wikipedia 已知向量、头部四类错误的先后顺序、
块头四类错误、NLEN 错误、尾部多余字节与校验和错误、所有截断前缀，
并在小流上遍历全部一刀两段/逐字节切分点、在块边界流上对结构偏移附近加密采样；
编码器输出始终与朴素整体实现逐字节对照，且用标准库 `compress/zlib` 做互通解码。
