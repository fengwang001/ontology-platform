# 差分同步校验与增量传输器设计

## 1. 两级校验和的分工推导

- 弱校验和采用加法型 16 位校验：`w = (Σ byte) mod 65536`。
  它可增量维护，碰撞常见（任意两字节 +1/−1 即碰撞）。
- 强校验和采用 SHA-256（取前 16 字节），仅在弱值命中候选块时计算一次，
  用于最终确认，抗碰撞。
- 只用弱校验和：碰撞块会被错误复用，目标拼出与源不同的数据却不报错。
- 只用强校验和：源端每个偏移都要算一次 SHA-256，复杂度
  `O(n·blockSize)`，滚动失去意义（弱校验和使总代价 `O(n)`）。
- 因此流程固定为：弱值筛候选 → 强校验和确认 → 复用，否则字面量。

## 2. 滚动推进公式

窗口 `[i, i+k)`：`w_i = Σ_{j=i}^{i+k-1} d[j]`。
推进一格：`w_{i+1} = w_i − d[i] + d[i+k]`（uint16 自然回绕）。
每次推进固定 3 次基本运算（减法、加法、进位隐式），初始化 k 次加法，
总运算次数 ≤ `k + 3·(n−k)` ≤ `4n`。

## 3. 包划分

- `chunk`：分块、弱/强校验和、`Roller` 滚动器（含运算计数器）。
- `sig`：目标签名（块号 → 弱值/强值）的生成、二进制编解码。
- `diff`：源端滑动匹配，输出补丁指令（引用块 / 字面数据）。
- `verify`：补丁编解码、CRC、四类截断分类、越界与整体一致性校验。
- `patch`：先整份校验通过后原子落地，否则目标文件保持不变。

## 4. 补丁指令格式

指令流（每条 5 字节定长，便于边界判定）：

- 引用：tag=1 + uint32 块号
- 字面：tag=2 + uint32 数据长度；数据体顺序拼接在 data 段

补丁文件布局：

```
header(52B): magic(4) ver(1) blockSize(4) srcLen(8) tgtLen(8)
             instLen(4) dataLen(4) srcHash(16) reserved(3)
inst 段(instLen 字节，5 字节整数倍)
data 段(dataLen 字节，字面数据依次拼接)
CRC32(4)，覆盖 header+inst+data
```

源端 diff：逐字节滑动窗口；弱值命中且强值相等则输出引用并跳过整块，
否则当前字节并入字面量段并前进 1 字节；末尾不足一块的全部作字面量。
连续字面量合并为一条指令。

## 5. 截断分类（四类，errors.Is 可区分）

按剩余长度 n 与 H=52、instLen、dataLen 判定：

1. `n < H`：ErrHeaderTruncated（头部不完整）
2. `H ≤ n < H+instLen`：ErrInstrTruncated（指令不完整）
3. `H+instLen ≤ n < H+instLen+dataLen`：ErrDataTruncated（数据段不完整）
4. 长度完整但 CRC 不符：ErrCRCMismatch

另定义 ErrBlockOutOfRange、ErrResultMismatch、ErrBadBlockSize。

## 6. 截断补丁不得部分应用

应用分两阶段：(a) 解析并校验整份补丁——结构长度、指令 5 字节对齐、
块号范围、字面长度与 data 段一致、CRC、整体 SHA-256 与 header 中
srcHash 一致；(b) 全部通过后在内存重建结果并以临时文件+rename 原子
替换目标文件。任一步失败立即返回错误，不触碰原文件，目标逐字节不变。
