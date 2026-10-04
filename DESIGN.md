# 设计说明（≤40 行）

## 结构
- `match`：纯函数式三态匹配，`Field{Value,Mask}` 两个字段；`New` 拒绝 `Value&^Mask!=0`。
  `Overlap` 用 `(v1^v2)&m1&m2==0`；`Contains` 判定 `myMask&otherMask==myMask && otherValue&myMask==myValue`。
- `flowtable`：哨兵错误 `ErrInvalid/ErrClockBack/ErrOverlap/ErrFull`（`errors.Is` 可判）。
  表项存 map + 序号；按 prio 分桶并惰性排序，Lookup 从高 prio 桶向下扫，桶内按序号升序命中即停。
- `bundle`：消息体定义在 flowtable（`AddMsg/ModifyMsg/DeleteMsg`），避免 bundle→flowtable 循环依赖。
  工作副本 = 深拷贝整表后逐条 apply；全部成功后用 copy-on-write 指针整体替换。

## 取舍与被放弃方案
- 同 prio 多命中不视为"未定义"：按安装序号最小者定胜负，保证重放确定。
- 批量提交不用"真表逐条执行后回滚"：回滚无法复活已驱逐/到期状态与序号，故采用工作副本整体替换。
- 优先级扫描不用持久有序索引（堆/树）：增删频繁且只需同 prio 内有序，惰性排序 O(k log k) 且只在桶变更后付一次；
  examined 上界天然成立：最坏考察"prio≥命中项"的全部表项，低 prio 桶永不触碰。
- 并发用单把 RWMutex：Lookup 间本可并行，但 lastHit 更新使 Lookup 也是写者；简化且满足串行等价。
- 超时在每个"被接受"操作入口统一 `expire(now)`：被参数/时钟拒绝的操作不落地到期、不推进时钟。
- 被驱逐/删除立即从 map 删除；`installed = len(entries) + 各原因移除事件数` 作为内部不变量自测。

## 验证
- `go build ./... && go vet ./...`
- `go test -race ./...`：表驱动用例（位边界、替换不占号、importance 恰等、idle/hard 同刻取 Hard、
  严格/非严格范围、批可见性与回滚、拒绝次序）。
- 1500 组随机序列：真实表逐项对照朴素线性扫描模拟，查动作/序号/事件/容量不变量，日志打印输入、输出与判定依据。
