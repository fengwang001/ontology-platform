# 多路径发送侧调度器（mptc）

纯事件驱动的多路径连接发送侧：连接级序号切段、普通/备用选路、连接窗口、
子流确认与连接确认分离、失效重注与重复副本。无时钟、无后台线程，标准库实现。

## 结构

- `mptc/scheduler.py` — 高效实现（deque + 段级最小堆 + 单锁）。
- `mptc/naive.py` — 独立朴素参考模型（全量列表/重扫，只用于差分测试）。
- `mptc/errors.py` / `mptc/types.py` — 错误码（固定优先级）与返回类型。
- `tests/test_scheduler.py` — 17 个定向用例。
- `tests/test_differential.py` — 1100 组随机事件差分（纳入 unittest）。
- `tests/difftest.py` — 差分框架与逐步日志生成器。
- `tests/bench_complexity.py` — 复杂度经验验证。
- `docs/design.md` — 设计说明、取舍、被放弃方案与复杂度论证。

## 用法

```python
from mptc import Scheduler, SubflowRole

s = Scheduler(mss=10)
s.add_subflow("a", rtt_ms=10, cwnd=20)
s.add_subflow("b", rtt_ms=20, cwnd=30, role=SubflowRole.BACKUP)
s.connection_ack(0, 1000)          # 通告连接接收窗口
r = s.write(b"hello world!")       # 事件后立即返回本次发出的段
for seg in r.segments:
    print(seg.seq, seg.length, seg.subflow, seg.reinjected)
s.subflow_ack("a", 10)             # 子流级累计确认（字节数，必须整段）
s.connection_ack(10, 1000)         # 连接级累计确认 + 新窗口
s.subflow_fail("a")                # 在途段升序重注，优先于新数据
s.subflow_recover("a")             # 窗口/RTT/在途重置为初始值
```

非法事件抛 `mptc.SchedulerError`，其 `code` 取 `mptc.ErrCode`，优先级为：
`INVALID_PARAM` → `SUBFLOW_NOT_FOUND` → `STALE_ACK` →
`ACK_NOT_SEGMENT_ALIGNED` → `ACK_OUT_OF_RANGE`；被拒绝事件不改变任何状态。

## 测试

```bash
python3 -m unittest discover -s tests -v
python3 -m tests.difftest --cases 1100 --steps 120 --log diff.log
python3 -m tests.bench_complexity
```
