# FINDINGS

## 组 1：event 编解码（go test ./event）

| 用例 | 结论 |
| --- | --- |
| 空载荷 / nil 载荷 / 普通 / 二进制 / 大序号 | 往返一致；空载荷记录定长 16 字节 |
| 截断到 15 字节 / 长度前缀虚高 / 翻转 CRC | 分别 ErrShortRecord、ErrLenMismatch、ErrCRCMismatch |

## 组 2：from 三种位置（stride=N，跳过数与索引使用情况）

| from 位置 | 跳过事件数 | 用上索引 |
| --- | --- | --- |
| （待测） | | |

## 组 3：500 事件段逐字节截断分类与 repair

| 字节区间 | 分类 | repair 前→后条数 |
| --- | --- | --- |
| （待测） | | |
