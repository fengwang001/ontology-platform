# 网关限流器设计说明

## 包分工
- `tier`：作用域 -> 等级。`Registry` 保存不可变的等级表（name/rank/T/B，name 与 rank 唯一）；
  `Resolve(scopes)` 忽略未登记的 `tier:*`（向前兼容），取 rank 最大者，无标记取 rank 最小者。
- `gcra`：纯算法。给定 T、B、cost、now、tat（tat<0 表示无 TAT），计算 a=max(tat,now)、new=a+cost*T，
  返回放行/限流及 Limit/Remaining/Reset/RetryAfter，不持有任何状态、不加锁，可独立复用与验证。
- `ratelimit`：`Limiter` 持有 `tier.Registry`、路由表（path->scope/cost）、每 sub 的 TAT、
  最大已接受时钟 maxNow、容量 S；单把互斥锁保证并发等价于某一串行顺序。

## 关键取舍
- 升级不重置、不截断 TAT（欠账保留）：令牌桶/GCRA 的负债是主体真实消耗，换付费等级不应凭空获得
  配额，也不应因降级而抹平历史；新等级只改变此后的 T、B 与代价。放弃方案：等级变化即清 TAT，
  会让反复升降级变成刷配额漏洞。
- 先鉴权后限流：403 不消耗任何配额、不建主体表条目，避免未授权请求探测并挤占他人桶。
  拒绝顺序固定为：参数 > 时间 > 时钟回退 > 路由 > 无等级 > 无权限 > 永远无法满足 > 表满 > 限流。
- 区分“永远无法满足”（cost>B，与时间无关，任何等待都无效）与“暂时限流”（等待后可放行，
  给 Retry-After），二者语义与客户端动作不同。放弃方案：统一报限流，会误导客户端无限重试。

## 跨包不变量
- 所有时间/配额量为 int64；now<=1e15、cost、T、B<=1e6，故 new<=1e15+1e12，不溢出。
- 只有放行才写 TAT=new，故 TAT<=放行时 now+B*T<=1e15+1e12；任何拒绝（含限流）不改任何状态。
- 表占用数为 TAT>now 的主体数；条目在 TAT<=now 时可惰性回收，回收对外不可见。
- 限流线：new-now<=B*T 放行；Remaining 地板除、Reset/RetryAfter 天花板除（整秒不进位）。

## 本地验证
- `go build ./...`；`gofmt -l .`；`go vet ./...`
- `go test -race -v ./...`（表驱动边界用例 + 2000 组随机序列对照朴素模拟，日志含输入/输出/依据）
