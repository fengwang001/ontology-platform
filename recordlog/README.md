# recordlog — 块对齐的记录分片日志

`recordlog` 把任意长度（上限 `MaxRecord = 1 MiB`）的记录切分为不跨块的片段，
写入固定大小（`8..65535` 字节）的块；读侧按字节流顺序还原记录，并在损坏时
给出四类可区分错误与确定性恢复位置。写出字节与偏移对同一追加序列**逐字节可
复现**。

## 磁盘布局

所有整数均为小端。

### 片段头（固定 7 字节）

| 偏移 | 长度 | 含义 |
| --- | --- | --- |
| 0 | 4 | CRC32（IEEE），对「类型字节 + 数据」计算 |
| 4 | 2 | 数据长度（0..65535） |
| 6 | 1 | 片段类型 |

片段类型：

| 值 | 常量 | 含义 |
| --- | --- | --- |
| 1 | `TypeFull` | 完整记录（一个片段写完全部字节） |
| 2 | `TypeFirst` | 首片（记录未写完） |
| 3 | `TypeMiddle` | 中片（非首片且未写完） |
| 4 | `TypeLast` | 末片（非首片且本片写完全部字节） |

### 块对齐与分片规则

设当前块剩余字节为 `rem`：

1. `rem < 7`：先在块尾补 `rem` 个 `0x00`，另起新块。
2. 本片数据长度 `n = min(rem - 7, 尚未写入的字节数)`。
3. 类型：
   - 第一个片段且 `n == 记录长度` → `full`；
   - 第一个片段且未写完 → `first`（**`rem == 7` 且记录非空时产生零长首片**）；
   - 非首片且未写完 → `middle`；
   - 非首片且本片写完 → `last`。
4. 空记录在 `rem >= 7` 时写一个零长 `full` 片段；`rem < 7` 时先补零再起新块。

`Writer.Append` 返回该记录**首个片段头的起始偏移**；若发生补零，该偏移
位于补零之后的新块起点。

## 写侧拒绝（不改变任何已写字节与偏移）

- 构造块大小 `B <= 7` 或 `B > 65535`：`ErrInvalidBlockSize`
  （`errors.Is(err, ErrInvalidBlockSize)`）。
- 记录长度 `> 1048576`：`ErrRecordTooLarge`，拒绝发生在任何写入之前。
- 底层 `io.Writer` 返回错误时原样透传。

## 读侧

`Reader.Next() (record []byte, offset int64, err error)`：

- 正常返回记录、记录首片段偏移、`err == nil`。
- 干净读到流尾：`err == io.EOF`。
- 损坏返回 `*CorruptError`，其 `Offset` 为**出错片段头的起始偏移**，
  `errors.Is(err, ErrXxx)` 可区分四类原因。

### 判定顺序（严格）

1. **片段头不足 7 字节或数据长度越出所在块**：仅凭头部即判
   `ErrLengthOutOfBlock`（长度检查）/ `ErrTruncated`（头部被截断），
   先于任何数据读取。块内剩余不足 7 字节视为填充跳过（即使流也恰好在
   填充区结束，属于干净 EOF）。
2. **数据字节不足**：声明长度的数据没有全部到达 → `ErrTruncated`。
3. **校验不符**：CRC32(type||data) 与头部不符 → `ErrChecksum`。
4. **类型序列非法**：
   - 类型值不在 1..4；
   - 没有进行中的首片却遇到 `middle`/`last`；
   - 记录未收尾时又遇到 `full`/新的 `first`。
   命中其一 → `ErrTypeSequence`。

另一种 `ErrTruncated`：流尾时一条 `first/middle` 记录没有被 `last` 收尾。

### 恢复规则

- 对**长度越界、校验不符、类型序列非法**：丢弃正在组装的记录（含触发
  错误的片段），跳过出错片段头所在块的剩余字节，从**该块的下一个块
  边界**继续扫描。下一次 `Next` 可能正常返回记录或再次报错。
- 对**流尾截断**（片段中途结束、记录未收尾）：该错误为终止性错误，
  返回出错片段头偏移；下一次 `Next` 必定返回 `io.EOF`。

## 并发

- `Writer` 内部互斥：`Append` 并发调用等价于某个串行顺序；
  相同追加序列得到逐字节相同的输出与偏移。
- `Reader` 内部互斥：多个 goroutine 并发 `Next` 等价于顺序消费，
  每条记录恰好被交付一次。

## 用法

```go
var buf bytes.Buffer
w, _ := recordlog.NewWriter(&buf, 32*1024)
off, _ := w.Append([]byte("hello"))

r, _ := recordlog.NewReader(bytes.NewReader(buf.Bytes()), 32*1024)
for {
    rec, recOff, err := r.Next()
    if err == io.EOF {
        break
    }
    if err != nil {
        var ce *recordlog.CorruptError
        if errors.As(err, &ce) {
            log.Printf("corrupt at %d: %v", ce.Offset, ce.Err)
        }
        continue // 非截断错误会自动跳到下一块边界
    }
    _ = rec // recOff == off for the first record
}
```

## 本地验证

```bash
# 全量测试
go test ./...

# 竞态检测 + 多轮
go test -race -count=3 ./recordlog/

# 详细日志（打印输入、输出字节与判定依据）
go test -v ./recordlog/

# 只跑损坏矩阵（每字节 8 种翻转、逐位置截断，均与朴素规则实现对拍）
go test -v ./recordlog/ -run 'TestFlipEveryByte|TestTruncateEveryPosition'

go vet ./...
gofmt -l .
```

测试要点：

- `TestRemaining0to8`：覆盖写入前 `rem = 0..8`（含 `rem=6` 补零、
  `rem=7` 零长首片、`rem=8` 一字节首片），输出字节与朴素实现逐字节对拍。
- `TestSpansThreeBlocks`：记录跨 3 个以上块，片段序列
  `first / middle* / last`。
- `TestEmptyRecords` / `TestRecordFillsBlock`：空记录与恰好填满块。
- `TestFlipEveryByte`：对输出每个字节逐位翻转（约 4k 次），不 panic，
  错误类别与恢复位置与朴素读完全一致。
- `TestTruncateEveryPosition`：0..len 逐位置截断，截断后下一次必 EOF。
- `TestTypeSequenceIllegal` / `TestLengthOutOfBlock`：针对性错误类别。
- `TestConcurrentAppend` / `TestConcurrentReader`：并发等价串行。
- `TestDeterministicReproduction`：同一追加序列两次输出逐字节相同。
