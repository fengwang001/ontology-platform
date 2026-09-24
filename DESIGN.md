# DESIGN: 前缀压缩有序字符串字典

## 1. 块格式（block 包）

```
header 16B: magic "PBLK" | K u32 | count u32 | nRestarts u32
重启点表:   nRestarts * u32，每个是该重启条目在块内的绝对偏移
条目区:     每条 = sharedLen uvarint | suffixLen uvarint | suffix 字节
CRC32 u32:  对以上全部字节的 IEEE 校验和
```

每 K 条设一个重启点：重启点条目 sharedLen=0 存全量；其余条目只存
「与前一条的公共前缀长度 + 后缀」。nRestarts = ceil(count/K)。

## 2. 重启点间隔 K 的权衡推导

- 取值代价：第 i 条只能从它所在的重启点 r=floor(i/K) 开始顺序解压，
  解压次数 = i - r*K + 1，上界为 K，平均 (K+1)/2。K 越大取值越慢。
- 压缩率：每个重启点付出「一条全量字符串 + 4B 偏移」，非重启点只付
  「2 个 uvarint + 后缀」。设平均串长 L、平均公共前缀 P，则每 K 条
  多存约 (P - 2) + 4 字节；K 越大重启点越稀，压缩率越高，K=1 时
  退化为不压缩（每条都是全量）。
- 二分代价：只能在重启点上二分（见第 3 节），比较次数上界
  4*ceil(log2(nRestarts)) + K：重启点数组二分每轮至多 2 次比较、
  再留一倍余量给边界判断，区间内线扫至多 K 次。K 越大线性段越长。
- 结论：K 是「取值/查找延迟」与「空间」的旋钮，默认取 16。

## 3. 为什么二分只能在重启点上做

非重启点条目的内容 = f(前一条)，脱离前驱无法独立解出；若直接对
全部条目二分，比较对象本身就算不出来，会拿到错误结果。因此查找
必须两阶段：先在重启点数组（可独立解码）上二分定位候选区间，
再在区间内顺序解码比较。块级同理：先用块目录的首末值二分定位
候选块，再进块。块级二分额外引入 4*ceil(log2(nBlocks)) 次比较。

## 4. 有序性检查的位置

- 编码时（block.Encode）：逐条断言 prev < cur（严格递增，不允许
  重复值），违反则拒绝并报告下标，如 `ab` 之后跟 `a`。
- 解码时（顺序解码路径）：同样逐条复查，保证「截断恢复出的最大
  前缀」仍然严格有序，半截后缀不会混出乱序条目。
- 公共前缀长度撒谎（sharedLen > len(prev)）在解码时检出，报
  ErrPrefixLen，不越界、不 panic。

## 5. 前缀长度按字节计

sharedLen 按**字节**计，不按码点。例：`café`(5B) 与 `cafés`(6B)
的公共前缀按字节 = 5（恰为前者全长），按码点 = 4。实现取字节：
无需 UTF-8 解码、O(1) 切片安全（共享的是前一条的字节前缀，拼回
时不会切断后者未共享部分的码点）。测试锁定 café/cafés 上两种
口径的数值差异，文档以此为准。

## 6. 字典格式（dict 包）

```
header: magic "PDIC" | K u32 | blockSize u32 | count u32 | nBlocks u32
目录:   每块 = offset u32 | length u32 | first 串 | last 串（uvarint+字节）
数据:   各块原始字节（块内自带 CRC）
```

字典只读，可整体落盘/载入；块惰性解码，解码块数可被计数（scan
测试据此断言只解压命中块）。

## 7. 截断分类与故障语义（verify/block）

按截断点落在哪个区域分类，均可 errors.Is 判定：

- ErrHeaderIncomplete：不足 16B 或 magic 错
- ErrRestartTable：重启点表被截断
- ErrEntryIncomplete：条目区被截断（返回最大可恢复前缀，仍有序）
- ErrCRC：条目完整但 CRC 缺失/不匹配
- ErrPrefixLen：sharedLen 撒谎
- ErrRestartOffset：重启点偏移错位；检出后回退为从块首顺序解压，
  结果仍正确，错误标注索引失效
- ErrUnsorted：违反严格递增

## 8. 并发

字典构建后不可变，查询（Get/Find/ScanPrefix）无共享可写状态，
计数器用 atomic；构建中的新字典以全新对象返回，未完成的块对
持旧引用的查询者不可见。`-race` 必须干净。
