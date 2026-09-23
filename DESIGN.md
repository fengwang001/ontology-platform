# DESIGN：火山算子流水线与提前终止回收

## 1. 算子与生命周期

算子接口三段式：`Open(ctx) error` 做资源准备；`Next(ctx) (Row, bool, error)` 返回一行，`ok=false` 且无错表示正常耗尽；`Close() error` 释放资源。
一元算子持有唯一 child；sorter 持有唯一 child；leaf 无 child。生命周期恒为 Open 至多一次、Close 恰好一次。

## 2. Close 责任与顺序（核心推导）

资源在树上是「父请求、子执行」的关系，因此：

1. **谁 Open 谁负责 Close**：父算子 Open 成功后必须保证自己的 Close 最终会调到 child。驱动者 `res.Run` 调用根算子 Close，各一元/sorter 的 Close 再关闭 child，形成自上而下传播链。
2. **顺序自上而下、恰好一次**：父先于子 Close，因为子的缓冲（sorter 溢出 fd、leaf 游标）可能正被父排空；父先停止生产，子才能安全停止。每个算子内部 `sync.Once` 守卫，重复 Close 幂等、不重复释放。
3. **Open 失败回滚**：Open 自下而上（父先 Open child 再初始化自身）。child Open 成功而父自身失败时，父立即 Close child 并返回错误，保证「Open 过 == Close 过」。
4. **Next 出错**：错误冒到驱动者后不再拉取，转为 Close 根，逐层关闭 child。已产出行不撤回（火山模型行已交付即生效）。
5. **Close 报错聚合**：Close 链不因错误中断——父记录自身错误后仍调 child Close。多错误用 `errors.Join` 聚合（首末都保留），测试用 `errors.Is` 逐个匹配。
6. **并发 Close/Next**：不 panic。算子以 closed 标志 + 互斥/Once 守卫，Next 检测到关闭返回哨兵 `ErrClosed`；sorter 溢出在 Close 时等其写完再统一删除，保证零残留。

## 3. LIMIT 下推

- 无排序：limit 计数达 k 后向父返回 ok=false，不再调 child.Next；扫描读取 ≤ k+1（第 k+1 次探测即停止），提前释放整棵子树。
- 有排序：排序是 blocking 算子，必须看完全部输入才能输出全局有序结果，故 sorter 读完全部输入（扫描读取 == 总行数），其上的 limit 只向上吐 k 行。
- **LIMIT 0**：采用「Open 下层后立刻 Close」，不调用任何 child.Next。理由：统一生命周期（Open==Close 恒等，记账无需特判跳过 Open），仅多一次廉价 Open/Close，零行读取。

## 4. sorter：内存上限与外部归并

- 配置 `memRows`：内存驻留峰值 ≤ memRows。攒满即把该 run 稳定排序后整体溢出为临时文件，随后清空继续。
- 溢出文件位于 `t.TempDir()`（生产 `os.MkdirTemp`），Close 时 RemoveAll，由 res 记账。
- 二进制格式：魔数 `"XSN1"`(4B) + 每行 `[uvarint 行体长][行体][CRC32 IEEE 4B]`；行体为各列 string 的 `[uvarint len][bytes]...`。
- 读回时每个溢出文件是一个有序归并段，配合内存段做 k 路堆归并（container/heap）；无溢出直接内存排序。键全相同时以稳定块序与堆 tie-break 保持输入顺序。
- 截断分类（逐字节尝试）：在魔数内 → ErrBadHeader；落在 uvarint 前缀区 → ErrBadLength；前缀声明的行体不足 → ErrBadBody；行体齐而 CRC 不符（含 CRC 尾被截）→ ErrBadCRC。任何截断必须中止报错，禁止少行结果。四类与 ErrClosed、Next 注入错误均经 errors.Is 区分。

## 5. res 记账与并发

`res.Tracker` 以原子计数记录 opens/closes 与 spillFiles（当前数、创建峰值），每棵树独立 Tracker；多棵树在多 goroutine 并发且共享 Tracker 时安全（原子操作）。用例结束断言 opens==closes、当前临时文件数==0。

## 6. 测试约束

全部表驱动，同类断言合入一个测试函数的一张表；截断点用 for 循环逐字节覆盖，不展开成多函数/多用例。
