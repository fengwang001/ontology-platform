# 设计说明：设备首次激活与重绑定服务

## 模块划分
1. `roster`：唯一可变状态拥有者。map[sn]记录（状态/id/gen/fp/until/tenant）、租户名额 map、全局 id 计数、全局时钟 maxNow、单把 sync.Mutex。guard 与 activate 不持有各自的锁，均在 roster 锁内调用，因此并发等价于某种串行顺序，无需细粒度锁/分桶。
2. `guard`：纯逻辑 + 按 sn 的 e/k/lockUntil。只提供 NoteConflict（命中第 M 次则锁定：k++、e=0、lockUntil=now+Lk·2^(min(k,7)-1)）与 Locked 判定；不看时钟推进，时钟语义集中在 roster/activate，避免两处时钟分叉。
3. `activate`：编排 Activate/Reset/Deactivate，实现拒绝次序与状态迁移。

## 关键取舍
- 拒绝次序：非法参数 > 时钟回退 > 未登记 > 锁定 > 冲突 > 截止 > 名额，按此短路；仅 ErrConflict 推进时钟并改 e/k/lockUntil，其余拒绝零副作用（时钟也不推进）。
- 锁定期连正确指纹的幂等重放也拒绝（ErrLocked），不放行：否则锁定将无法阻断重放通道；锁定判定先于指纹比较。now==lockUntil 即解锁。
- Registered 下任何指纹都算首次绑定，不产生冲突、不累加错误。
- Reset 后处于 ResetPending：重绑沿用 id、gen+1，不看截止/不占新名额（名额始终占着）；这是被放弃方案“重置后须重新走截止与名额”的明确反例（见题例 until=1000、now=2000 重绑成功）。
- Deactivate 释放名额、清指纹、状态回 Registered，保留 id/gen 与 guard 的 e/k；再次激活必须重新通过截止与名额判定，成功时 id 沿用、gen+1。
- 幂等重放成功不清零 e（e 只随第 M 次错误清零或随 Reset 清零）。
- gen 即历史成功绑定指纹次数：首次激活=1，此后每次成功绑定 +1。

## 探查点（probes）
- roster 内部计数：map 访问记 1，名额读/写记 1。Activate 至多 sn 记录 1 次 + 名额 1 次 = 2；名额判定用 tenants map O(1)，不扫描名单。RegisterBatch 预扫描每 sn 1 次，接受时每 sn 写 1 次，≤2×批长；拒绝在写入前中止，零痕迹。

## 并发
- 全局互斥使“同一 sn 并发首次激活”自然串行：先到者绑定，同指纹后来者得幂等结果、异指纹者 ErrConflict；名额占用恒等于 Activated+ResetPending 数且不越界；id 连续无洞。

## 本地验证
- `go test -race -v ./...`：表驱动用例覆盖取等边界、第 M 次返回值与锁定时刻、封顶、Reset 清 e 不清 k、重绑不看截止、停用沿用 id、整批拒绝零痕迹、拒绝次序、probes 100/10000 对照。
- 1500 组随机操作序列：逐步朴素参考模型对照输出，并在 -v 日志打印输入/输出/判定依据；另跑高并发 goroutine 校验不变量。
