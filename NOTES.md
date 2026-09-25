# MLFQ 调度器笔记

## 三、配额记账推导（防博弈的关键）

- 现象：博弈作业每次跑到配额用完前一刻就 `Yield`，永远留在最高级。
- 假设配额"每次被调度时重新计数"：作业跑 Q[0]-1 刻后 `Yield`，下次被调度时
  已用刻数从 0 重算，永远到不了 Q[0]，于是永不降级，最高级被它独占。
- 结论：配额必须是"作业在本级累计已运行的刻数"，账记在作业身上而非某一次
  连续调度上。`Yield` 只把作业移到本级队尾，不清账；只有降级（换级，账随级
  走）与提升（账务周期结束）才清零。
- 钉住：Q[0]=4，博弈作业每次跑 3 刻就 `Yield`，第 2 次被调度的第 1 刻累计
  used=4，立即降到第二级；对照的错误实现让出即清零，永不降级（TestGaming）。

## 二、语义位置与测试（测试全在 check/check_test.go）

1. 选择规则：`mlfq.Step`（mlfq/mlfq.go）扫级取队首，同级 FIFO 由 level 包保证 — TestRandomRef
2. 逐步一致：`check.Naive`（check/check.go）线性扫描对照，种子 42 跑 500 步 — TestRandomRef
3. 提升：`Step` 末尾 `tick%Boost==0` 调 `boost`；等待上界 B=S+(MaxJobs-1)·Q[0] — TestBounds
4. 守恒：`Step` 中 `left` 归零即删除，每作业运行刻数==提交工作量 — TestConcurrent
5. 错误：`Submit`/`Yield` 返回哨兵错误，先校验后修改故零副作用 — TestErrors

## 四、Step 检查队列数实测

- 上界 L+1=4：10000 个作业下实测 Checked=1，空调度器实测 3，与作业数无关 — TestBounds
- 等待上界实测：B=21（S=7、MaxJobs=8、Q[0]=2），400 刻内最大等待 maxGap=13 ≤ 21
