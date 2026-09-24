# base32 推导与不变量

## 第三节推导：末尾填充的合法个数

5 字节 = 40 位 = 8 个 5 位字符。末组剩余 r 字节即 8r 个有效位，
有效字符数 = ceil(8r/5)，填充个数 = 8 - 有效字符数：

| 剩余字节 r | 有效位 8r | 有效字符 ceil(8r/5) | 填充 `=` |
|---|---|---|---|
| 1 | 8  | 2 | 6 |
| 2 | 16 | 4 | 4 |
| 3 | 24 | 5 | 3 |
| 4 | 32 | 7 | 1 |

合法填充集合（含整组时的 0）：**{0, 1, 3, 4, 6}**。

不可能值 **2 与 5**：填充 2 意味着 6 个有效字符 = 30 位，填充 5 意味着
3 个有效字符 = 15 位；30 与 15 都不是 8 的倍数，装不下整数个字节，
任何真实字节串都编不出它们，故解码遇到必须报 ErrPaddingCount，不得容忍。

## 四条不变量的落实位置与钉住它们的测试

1. 往返恒等：b32.go 的 encodeGroup/decodeGroup 位运算互逆（40 位打包/还原）；
   测试 `TestRoundTrip`（长度 0–20 循环逐字节比对）。
2. 编码长度确定：b32.go `Encode` 中 `make([]byte, groups*8)`，填充只在末组写入；
   测试 `TestRoundTrip` 断言长度与合法填充，`codec.SelfCheck` 复核。
3. 非法输入可判定：b32.go 的哨兵错误 ErrInvalidChar / ErrPaddingPos /
   ErrPaddingCount（含长度非 8 倍数）；测试 `TestDecodeErrors`。
4. 失败不留痕：b32.go `Decode` 所有出错路径一律 `return nil, err`，
   输入 string 不可变、绝不回写；测试 `TestDecodeErrors` 断言结果为 nil
   且原串不变、拒绝后编解码器仍可用。
