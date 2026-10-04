# 设计说明：数据集弃用→下线分阶段闸门

## 分层
- `registry`：只存数据集 DAG、阶段、计划时刻（sunsetAt/brownStart）与延期计数。无时钟、无并发，由上层加锁调用。
- `usage`：每个数据集独立维护消费者状态（lastAccess、确认时间戳）。按 lastAccess 升序维护双链表 + map 定位节点；新访问用 MoveToBack（时钟单调故时间戳单调）。活跃消费者从表尾向头枚举，遇到 lastAccess<=now-Q 即停：扫描数 = 活跃数 + 至多 1 个僵尸，历史消费者再多也不扫。
- `sunset`：配置、单调时钟 lastNow、拒绝次序编排与演练窗口公式 o < min((i+1)X, Pd)。单把 sync.Mutex 包住全部公开操作，保证并发等价于某一串行顺序（粗锁取舍：数据规模小、实现简单、杜绝跨数据集死锁；已评估按数据集分锁，但 Advance 要读下游状态，多锁顺序复杂，放弃）。

## 关键取舍
- 阶段只由显式 Advance 改变，绝不随 now 自动切换：Deprecated 过了 brownStart 仍按 Deprecated 放行（仅 Warning）。放弃“惰性自动推进”——它会让同一 now 下 Access 结果依赖调用顺序，不可复现。
- 确认后再成功访问使确认作废（确认时间戳与 lastAccess 比较）。放弃“一次 Ack 永久迁移”：消费者回到旧数据源必须重新确认，否则会被永远漏报。
- 被拒绝操作不改任何状态，含全局时钟（含 ErrBrownout/ErrRetired 的 Access）。因此先做全部校验、最后统一写状态并推进 lastNow。
- 拒绝次序固定为：参数非法 > 时钟回退 > 数据集不存在 > 阶段错误 > 操作自身错误；每类错误为哨兵错误，附加明细用 fmt.Errorf("%w: ...", ErrX)，errors.Is 可判定。
- 延期只允许 Deprecated；撤回弃用不清零延期次数/累计量，防止“撤回-重弃用”刷额度。
- 下游阻塞检查先于消费者检查（ErrDownstream > ErrConsumers），且只看直接下游（递归保证由下游先于上游 Retired 的推进序实现）。
- Deprecate 返回的影响清单为全部传递下游中未 Retired 者，沿入边闭包后按字节序排序。

## 本地验证
- `go test ./...` 表驱动覆盖：各周期边界秒（o==kX 与 kX-1）、拒绝段加长至整周期、被拒不记 lastAccess/不作废确认、确认后再访问重新活跃、lastAccess==now-Q 不活跃、下游阻塞优先、Brownout 不可撤回/延期、累计恰等 Xmax、拒绝次序。
- 1500 组随机操作序列与朴素全量扫描模型逐结果对照（含每步活跃集合），日志打印输入/输出/判定依据；另测 100 vs 10000 僵尸消费者的 scanned 上界（<=活跃数+1）。
- `go test -race ./...`、`go vet ./...`、`gofmt -l .`。
