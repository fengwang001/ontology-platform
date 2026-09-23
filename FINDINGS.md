# 测试结论（随实现逐组补充）

## event 编解码

- 记录 = len(4) + payload + crc32(4)，空载荷合法，64KiB 载荷往返一致。
- 截断分类：不足 4B → `ErrLengthIncomplete`；payload 不足 → `ErrBodyIncomplete`；
  CRC 字段缺失或被篡改 → `ErrCRCMismatch`，均可 `errors.Is` 判定。

## 表 1：三种 from 位置的定位行为

（待 replay 测试后填写）

## 表 2：截断点分类与 repair 改正

（待 repair 测试后填写）
