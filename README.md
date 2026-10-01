# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 窗口看门狗

`watchdog.New(t0, open, close, pre)` 创建整数时间看门狗：

- 初始上次喂狗时间为 `f=t0`，合法参数满足 `0 <= open < close`、`0 <= pre < close`。
- 操作时刻的经过时间为 `d=t-f`；有效喂狗窗口是左闭右开的 `open <= d < close`。
- `d < open` 的喂狗在操作时刻 `t` 立即复位，复位原因是 `Early`，并且该喂狗被拒绝。
- 无论是否有人操作，`d=close` 都在 `f+close` 自动复位，原因是 `Timeout`。
- `pre > 0` 时在 `f+close-pre` 产生一条预警；`pre=0` 不预警。
- 有效 `Feed(t)` 令 `f=t` 并重新武装预警；`Restart(t)` 只在复位态有效，成功后以 `t` 开始新周期。

### 事件与操作顺序

每个 `Feed(t)`、`Tick(t)`、`Restart(t)` 都先补记到点事件：

1. 将所有时间不大于 `t`、尚未记录的预警和超时复位按时间加入事件表。
2. 同一周期先预警后复位；`Timeout` 时预警严格早于复位。
3. 预警可能早于 `open`，此时过早喂狗仍会先补预警，再在同一操作中产生 `Early` 复位。
4. 到点事件全部记录后，才判定本次 `Feed` 或 `Restart` 是否有效。

因此在 `d=close-pre` 的喂狗会先得到预警再更新 `f`；在 `d=close` 的喂狗会先补记 `Timeout` 复位，然后因处于复位态被拒绝。超时刚好到点时调用 `Restart(f+close)`，会先补记超时，再成功重启。

### 拒绝原因与状态

`Outcome.Rejection` 区分以下拒绝：

- `time rollback`：操作时刻小于上一次操作时刻，首个操作与 `t0` 比较；不补事件、不改变任何状态。
- `feed while reset`：复位态喂狗；保留本次已补记事件，但喂狗不改变看门狗状态。
- `early feed`：有效时间窗口之前喂狗；保留已补记事件并触发 `Early` 复位，是唯一会改变看门狗状态的拒绝。
- `restart while armed`：未处于复位态时重启；保留已补记事件，但不重启。

除时刻回退外，拒绝仍会把上一次操作时刻推进到本次 `t`。复位后门闩锁存，直到有效 `Restart`；锁存期间不会产生新预警或新复位。`Events()` 返回事件表副本，全局事件表时刻非递减，相同操作序列重放得到相同结果。

### 注入时钟

显式时间方法用于确定性建模和测试：

```go
w, _ := watchdog.New(0, 2, 5, 1)
w.Tick(3)
w.Feed(4)
w.Restart(8)
_ = w.Events()
```

需要从时钟取时时，实现 `watchdog.Clock` 并使用 `NewWithClock`：

```go
w, _ := watchdog.NewWithClock(0, 2, 5, 1, clock)
_, _ = w.TickNow()
_, _ = w.FeedNow()
_, _ = w.RestartNow()
```

## 本地验证

```bash
# 全量测试
go test ./...

# 打印随机重放的输入、输出和逐步朴素模拟判定依据
go test -run TestRandomReplayMatchesNaiveModel -v ./watchdog

# 并发安全
go test -race ./...

# 格式化与静态检查
gofmt -w .
go vet ./...
```
