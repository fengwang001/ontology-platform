# NOTES

## 一、四字段低位在前打包推导（f0..f3，总宽 14 位）

| 字段 | 宽度 | 有符号 | 值 | 位范围 | 贡献（值 << 偏移） |
|---|---|---|---|---|---|
| f0 | 3 | 否 | 5 | [0,3) | 5<<0 = 5 |
| f1 | 5 | 否 | 18 | [3,8) | 18<<3 = 144 |
| f2 | 4 | 是 | -3（补码 1101=13） | [8,12) | 13<<8 = 3328 |
| f3 | 2 | 否 | 1 | [12,14) | 1<<12 = 4096 |

打包结果：5+144+3328+4096 = **7573 = 0x1D95**（二进制 0001 1101 1001 0101）。

**(甲)** 高位在前（第 1 个字段占 14 位使用区的最高 3 位，f0 偏移 11、f1 偏移 6、f2 偏移 2、f3 偏移 0）：
5<<11 + 18<<6 + 13<<2 + 1 = 10240+1152+52+1 = **11445 = 0x2CB5**；正确（低位在前）为 **7573 = 0x1D95**。

**(乙)** f0 宽 3 赋值 9：9 > 2^3-1=7，正确实现**拒绝并报溢出错误，不打包**；naive 静默截断取低 3 位 9&7=**1**，会存成 1。

**(丙)** f2 取出的 4 位是 1101，最高位为 1，符号扩展后 = **-3**；不做符号扩展按零扩展则得 **13**。

## 二、四条不变量：保证位置与钉住测试

1. 往返一致：bits.PackFields 只写掩码内位、bits.Extract 按符号位扩展（bits/bits.go）；测试 `TestRoundTrip`（bits/bits_test.go）。
2. 无重叠无空洞：偏移=前缀宽度累加、总宽≤64 校验（bits/bits.go PackFields）；测试 `TestLayout`。
3. 与朴素参照一致：测试内独立手写 value<<off 累加对照；测试 `TestNaiveReference`。
4. 失败不留痕：PackFields 先全量校验再累加、位图先验下标再动字节（bits/bits.go）；测试 `TestRejectedNoSideEffect`。

自检：`api.API.SelfCheck()` 对内置向量核验上述四条；测试 `TestSelfCheck`（bits/bits_test.go）。
定位 O(1)：pack.Bitmap 用 下标/8 与 1<<(下标%8) 直接定位，非导出计数器 probed 每次仅 +1；测试 `TestLocateConstantProbes`（pack/pack_test.go）。
并发：pack.Bitmap 互斥锁保护，N goroutine 置不同位；测试 `TestConcurrentSetDistinctBits`（pack/pack_test.go）。
