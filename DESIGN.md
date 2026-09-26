# 只读快照一致性导出器设计（标准库，临时目录）

## 模型
- store.Store 是内存 KV：单调版本号 version；Put(k,v) 使 version++。
- Store 维护一组快照钩子（snapshot.Handle 实现 store.SnapshotHook）。
- snapshot.Open(store, age) 在当前版本 V 冻结键集合 keys(V)，返回句柄。

## 1. 写时复制（COW）触发点
Put(k,v) 前，若存在打开快照 S 且 version 位于 [S.begin, S.end=begin+age)，
把"V 时刻可见的旧值"复制进 S.ret[k]，再写新值。快照读键时优先 ret，
否则读当前 map——导出永远看到 V 时刻视图，与物理覆盖时机无关。
新键（V 不存在）永不进入快照，即便导出期间创建。
触发点必须在覆盖之前，且复制独立字节切片，防止别名污染。

## 2. 保留值释放：为何按最老快照判断
每个 Handle 自带 ret，只保留"自己版本之后、失效之前"被覆盖的值；
更早的覆盖在 begin 之前（读不到），更晚的在 end 之后（读不到）。
故 Handle.Close 可整体释放该句柄全部保留值，计数立即归零，无需扫描。
两个重叠快照 S1(V1)、S2(V2>=V1)：同一次覆盖可同时落入各自窗口，
旧值分别保留进 ret1、ret2。S1 关闭只释放 ret1；只有 S2 也关闭，
ret2 才释放，总计数才归零。这就是"最老快照"语义：以窗口下沿 begin
判最老；只要存在 begin<=version<begin+age 的句柄，覆盖就必须为它保留，
每个句柄只对自己的生命周期负责。

## 3. 导出顺序无关性
快照视图 k→v 是不可变纯函数。每条形如 enc(k)||enc(v) 的长度前缀帧
（enc(x)=u32 大端长度 + 字节）。清单 TotalCRC 先按键排序再对规范帧逐帧
CRC32 聚合，故只是帧集合的函数；分块只按字节切分，影响的仅是块边界。
字典序与逆序导出因此 TotalCRC 相同、内容集合逐字节相同。

## 4. 文件布局、自描述块与续传
文件 = manifest 区 || block 区。
manifest 区：4 字节大端长度 L || L 字节 JSON。
块 = 头 16 字节（Index u32, Total u32, BodyLen u32, HeaderCRC u32）
|| 体 BodyLen 字节。头 CRC 覆盖前 12 字节；体 CRC 存清单 Blocks[i].CRC，
避免把"头/体不完整"误判成 CRC 错。
清单含 SnapshotVersion、BlockSize、Blocks[{Index,Offset,Len,CRC}]、
TotalCRC、Complete（收尾置 true）。
续传：读清单定位第一个"文件长度 < Offset+16+Len"的块 k，截到 Offset，
从块 k 重写其后全部块。

## 5. 截断分类（errors.Is 四类）
- [0,4) 或 [4,4+L)：清单长度/JSON 不全 → ErrManifestIncomplete
- 块 k [off,off+16)：头不足 → ErrBlockHeaderIncomplete
- [off+16,off+16+len)：体不足 → ErrBlockBodyIncomplete
- 长度足够但字节被改：头 CRC / 体 CRC 失败 → ErrCRC
区间互不重叠，按文件长度落点唯一分类；清单缺失不可续传，否则从 k 续。

## 6. 快照过期为何拒绝续传
已存在块是版本 V 读出的；Handle 关闭后保留值已释放，再读会得到当前数据，
拼出跨时刻文件且总校验可能偶然通过。故 Resume 要求同一未关闭 Handle：
Closed 即返回 ErrSnapshotExpired，且调用前不截断，已导出字节原样保留。

## 7. 资源约束
- COW 内存 O(窗口内被覆盖的不同键数)，与总键数无关：Handle.Retained()。
- 每键恰好读一次：Get 计数 Reads()，导出器对 keys 各调一次。
- 块缓冲不超过一块：PeakBlockBytes 记录峰值。

## 8. 并发与关闭竞争
Store 用 RWMutex；Handle 用 mutex 护 ret/closed，WaitGroup 跟踪导出。
Close 先置 closed（新导出得 ErrSnapshotExpired），Wait 已有导出结束，
再释放 ret。只有全部块写完并校验通过才置 Complete=true，半截文件不会
被当成完整。

## 9. 边界
空存储（0 键，TotalCRC=0）、单键、全量改写（Retained==键数）、
BlockSize>总数据（单块）、BlockSize==0 → ErrBlockSize、空键合法、
空值 []byte{} 合法并以长度前缀帧与"键不存在"（无帧）区分。
