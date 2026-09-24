# FINDINGS：测试结论

## 已完成测试组
- event：空/非空载荷帧往返一致；帧截断按位置分别归类 ErrLength / ErrBody /
  ErrCRC，载荷或 CRC 翻位均为 ErrCRC。

## 表①：三种 from 位置（N=128，事件 0..99999）

待 sparse/replay 测试后填写。

## 表②：500 事件段逐字节截断分类与 repair

待 repair 测试后填写。
