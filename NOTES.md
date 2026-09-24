# NOTES

## 三、11 步推导（哈希写作 (FK,Val)；队列元素 (k,(FK,Val),RVal)；初始右表 A→a1、B→b1）

| # | 操作 | sub 之后 | 队列A | 队列B | 投递 | 输出 | 结果之后 |
|---|---|---|---|---|---|---|---|
| 1 | PutLeft(k1,A,x) | A:{k1:(A,x)} | (k1,(A,x),a1) | 空 | — | 无 | {} |
| 2 | PutLeft(k1,B,x) | B:{k1:(B,x)} | (k1,(A,x),a1) | (k1,(B,x),b1) | — | 无 | {} |
| 3 | Deliver(B) | 同上 | (k1,(A,x),a1) | 空 | 采纳(k1,(B,x),b1) | +k1=(x,b1) | {k1:(x,b1)} |
| 4 | Deliver(A) | 同上 | 空 | 空 | 丢弃(k1,(A,x),a1) | 无 | {k1:(x,b1)} |
| 5 | PutRight(B,b2) | 同上 | 空 | (k1,(B,x),b2) | — | 无 | {k1:(x,b1)} |
| 6 | PutLeft(k1,"",x) | 全空 | 空 | (k1,(B,x),b2) | — | -k1 | {} |
| 7 | Deliver(B) | 全空 | 空 | 空 | 丢弃(k1,(B,x),b2) | 无 | {} |
| 8 | PutLeft(k2,B,y) | B:{k2:(B,y)} | 空 | (k2,(B,y),b2) | — | 无 | {} |
| 9 | DeleteRight(B) | 同上 | 空 | (k2,(B,y),b2),(k2,(B,y),nil) | — | 无 | {} |
| 10 | Deliver(B) | 同上 | 空 | (k2,(B,y),nil) | 采纳(k2,(B,y),b2) | +k2=(y,b2) | {k2:(y,b2)} |
| 11 | Deliver(B) | 同上 | 空 | 空 | 采纳(k2,(B,y),nil) | -k2 | {} |

- (甲) 第 4 步投递的是第 1 步订阅 A 时入队的 (k1,(A,x),a1)；第 2 步 k1 已改 FK=B，当前哈希 (B,x)≠(A,x)，故丢弃。若不校验哈希：第 4 步输出 +k1=(x,a1)，k1 结果变为 (x,a1)，而此刻 k1 的 FK 是 B——连到了 A 的值，结果错误。
- (乙) 第 6 步输出 -k1。第 7 步投递的是第 5 步 PutRight(B,b2) 对订阅者 k1 生成的响应；第 6 步 k1 的 FK 已改 NULL，哈希 ("",x)≠(B,x)，故丢弃。若不校验哈希，11 步后结果表为 {k1:(x,b2)}（k1 的 FK 是 NULL 却留在结果里）。
- (丙) 第 9 步只更新右表并向订阅者入队 nil 响应，输出只能由投递产生，故不输出。k2 的结果第 10 步出现、第 11 步被 nil 响应撤回。若把 nil 响应当「什么都不做」：第 11 步无输出，11 步后结果表为 {k2:(y,b2)}，而右表 B 已删、批量重算为空——违反不变量 1（与批量重算一致）。

## 二、四条不变量的保证位置与钉住它的测试

1. 批量重算一致：响应只在哈希匹配时改写结果（lside.Deliver），nil 响应删结果；TestBatchEquivalence、TestElevenSteps。
2. 变更日志自洽：-k 仅在结果含 k 时输出、+k 仅在值变化时输出（lside.PutLeft/DeleteLeft/Deliver）；TestChangelogPrefix。
3. 订阅一致：订阅/退订与左行写入同事务推进（lside.PutLeft/DeleteLeft，lside.CheckSubs 校验）；TestSubConsistency。
4. 失败不留痕：所有变更先校验（键非空、CanEnqueue 预检、队列非空）再动手（rside.Put/Delete/Subscribe、lside.PutLeft）；TestFaultInjection。
