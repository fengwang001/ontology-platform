# MLFQ 防博弈设计笔记

## 第三节推导：配额按什么记账才防博弈
- 事故复现：若「每次被调度时配额重新计数」，博弈作业每次连续跑 Q[0]-1 刻
  就 Yield 回队尾；再排到队首时配额又从 0 计，于是永远用不满 Q[0]、
  永不降级，长期霸占最高级，把同级其他作业挤得跑不动。
- 正确记账：配额按「在本级累计已用刻数」记账，挂在作业上而非一次连续
  服务上。Yield 只改排队位置，不清零已用配额；仅两处清零：降级进新一级、
  提升周期 S 到点全员回最高级。
- 效果：Q[0]=4 时博弈作业第一次服务跑 3 刻 Yield，第二次被调度再跑 1 刻
  即累计满 4 刻，在第二次服务期间降级；重置式记账下每次服务都是 3 刻，
  永不降级。TestGaming 钉住正确实现，并内联重置式错误实现作对照。

## 第二节语义落点（代码位置 / 测试函数）
1. 选择规则：mlfq/mlfq.go Step（L48）+ level/level.go；TestMatchesNaive
2. 逐步一致：check/check.go Naive.best（L27）线性扫描；TestMatchesNaive（种子 42，500 步）
3. 提升：mlfq/mlfq.go boost（L90）；等待上界 B=S+n·Q[0]；TestBoostBound
4. 守恒：Step 每刻 remain-1、归零即删除；TestConcurrentConservation
5. 错误：ErrDuplicate/ErrUnknown/ErrFull（mlfq/mlfq.go L10-12）先校验后改动、
   失败零副作用；TestErrors

## 第四节实测
- 10000 个作业（全在最高级）单次 Step 检查队列数 = 1，上界 L+1 = 4；
  断言在 TestConcurrentConservation 尾部。计数器 last 为非导出字段，
  经 Stats.LastCheck 暴露；Step 只按级扫描队列，与作业数无关。

## 第五节并发
- 2000 个 goroutine 并发 Submit、主 goroutine 不停 Step，go test -race
  干净；结束后每个作业运行刻数恰好等于提交工作量（同上一测试）。
