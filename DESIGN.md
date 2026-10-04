# 设备首次激活与重绑定服务 — 设计说明

## 数据模型
- roster 持 map[sn]*record（record 含 batch/tenant/until/state/id/gen/fp），nextID 全局连续分配。
- guard 持 map[sn]*entry（e 错误计数、k 历史锁定次数、lockUntil）；锁定公式 now+Lk*2^(min(k,7)-1)，k 为加一后的值。
- activate.Service 组合 roster+guard+租户名额 used 与 N 两个 map；已占名额即 used，不扫描名单。

## 并发与时钟
- 两层锁：roster.mu（名单、时钟 maxNow 归属 roster）与 svc.mu（guard、名额）；固定加锁顺序 roster→svc，嵌套获取无环，故线性化安全。
- RegisterBatch 只取 roster.mu；Activate/Reset/Deactivate 先持 roster.mu 校验名单/状态/截止，再嵌套 svc.mu 完成 guard 与名额；同 sn 并发首次激活因此串行，唯一指纹胜出。
- 时钟两阶段：先 peekClock 比对（回退不改任何状态），操作生效时 commitClock；ErrConflict 是唯一改状态的拒绝（推进时钟并写 guard），其余拒绝零副作用。

## 取舍与放弃的方案
- 锁定期内连指纹正确的幂等重放也报 ErrLocked（而非放行）：锁定是对 sn 的整体冻结，保证锁定期内该 sn 无任何成功 Activate；且幂等成功不清 e，避免重放稀释错误计数。
- Reset 后重绑（ResetPending→Activated）不看截止、不占新名额：名额首次占用后保留至 Deactivate；截止只约束 Registered 的（重新）首次激活。
- Deactivate 保留 id/gen、清 fp、释放名额、回 Registered：再次激活须重过截止与名额；成功绑定时沿用旧 id、gen+1。
- Reset 只清 e/解除锁定，保留 k：指数退避的封顶历史跨重置延续。
- 拒绝严格按序只报第一个：Invalid > ClockBack > Unknown > Locked > Conflict > BatchClosed > Quota；Registered 不比对指纹，因此不累计错误、不触发锁定。
- RegisterBatch 全有或全无：先纯读查重，报下标最小重复项（批内重复取后一次出现下标），确认无冲突后统一写入，失败零痕迹。

## Probes
- roster 内非导出计数器：Find 的 map 探查 +1；Activate 路径只按 sn 定位 1 次（≤2），名额判定为 map 查表 O(1)（100 与 10000 档对照）；RegisterBatch 每 sn 查 ≤2 次（批内去重 map + 名单 map）。

## 本地验证
- go build ./...；go test -race -v ./...；gofmt -l .；go vet ./...
- 测试：表驱动场景（until/lockUntil 取等、第 M 次返回与锁定时刻、封顶、Reset、重绑、停用沿用 id、拒绝次序、零痕迹），1500 组随机序列与朴素模型逐步对照（打印输入/输出/判定依据），同序双重放确定性比对，同 sn 并发首激活互斥与名额不变量，100/10000 probes 对照。
