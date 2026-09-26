# NOTES: ontology-689 Base64（标准字母表 + 填充）

## `foobarb` 三行分步表（块字节 | 切出的 4 个 6 位值(十进制) | 字符）
1. `66 6F 6F` (foo)    | 25, 38, 61, 47 | `Z m 9 v`
2. `62 61 72` (bar)    | 24, 22, 23, 50 | `Y m F y`
3. `62 00 00` (b,补0)  | 24, 32,  0,  0 | `Y g = =`

完整编码串：`Zm9vYmFyYg==`

## (甲)(乙)(丙)
(甲) 编码单字节 `0x66`：6 位组 25,32 → `Zg==`，含 **2 个 `=`**。若固定只补 1 个 `=` 会错成 `Zg=`（3 字符）：长度非 4 的倍数，哨兵 `ErrLength` 整体拒绝。
(乙) `Zm9=`：最后一个 6 位组 61=`111101`，低 2 位是 `01`，应为 `00` → 哨兵 `ErrUnusedBits`，返回 `(nil,err)`；naive 不校验会静默解出 `fo`（66 6F），垃圾位被吞。
(丙) `TWFu` 正常解为 `Man`。`TWE` 长度 3 非 4 倍数 → 正确实现 `(nil, ErrLength)`；naive 自动右补 `=` 成 `TWE=`（末组 4=`000100`，低 2 位恰为 0），误解出 `Ma`——残串被当成合法输入。

## 四条不变量：代码位置 → 钉住的测试函数
1. 往返一致：`b64.Decode` 逐块取 24 位还原（b64/b64.go）→ `TestRoundTrip`
2. 规范输出：`encodeBlock` 余 2 补 1 个、余 1 补 2 个 `=`（b64/b64.go）→ `TestEncodeCanonical`
3. 与朴素参照一致：测试内手写 `naiveEncode` 逐 3 字节查表，全向量字节级对照 → `TestTextbookMatch`
4. 失败不留痕：`Decode` 先做完全部校验再分配输出，非法即 `(nil,err)`（b64/b64.go）→ `TestDecodeErrors`、`TestReusableAfterError`

另：O(1) 定长寻址 `data[4k:4k+4]`、非导出计数 `lastBlockRead` 恒为 4（codec/codec.go）→ `TestBlockLocationConstantRead`；并发与 `SelfCheck`（api/api.go）→ `TestConcurrentEncodeDecode`、`TestSelfCheck`。
