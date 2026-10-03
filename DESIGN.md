# 紧急访问授权服务设计说明

## 总体取舍

- **先放行后评审，而非先审批**：紧急场景下等待审批会失去意义，
  因此 Request 成功即生效（start=now），把安全性交给事后评审、
  评审窗口 Rw 与锁定期 Lk 来兜底。被放弃的方案是"预审批白名单"，
  它无法满足"任意时刻紧急放行"的可用性要求。
- **逾期自动失效，而非自动续期**：无评审的授权在 start+Rw 自动截止
  （effEnd 取 min(nominalEnd, start+Rw)），沉默即拒绝，避免授权
  被遗忘后长期有效。被放弃的"自动续期"会把举证责任倒置给安全方。
- **策略按请求时刻快照，而非追溯改写**：每个授权保存当时的
  Rw/Lk/PolicyVer，SetPolicy 只影响新授权。被放弃的"全局最新策略
  追溯生效"会使历史判定随配置变化，破坏可复现性。

## 结构与不变式

- 三个包：policy（纯函数：路径/名称/作用域校验、covers、策略校验）、
  ledger（仅追加的顺序账本，Seq 从 1 无洞）、grant（Grant 与 Service）。
- 时钟：Service 记录已接受的最大 now；被拒绝的操作不写账本、
  不推进时钟，保证账本只含已接受操作，重放逐条一致。
- effEnd：驳回 min(nominalEnd, rejectedAt)；批准 nominalEnd；
  无评审 min(nominalEnd, start+Rw)。恒有 start ≤ effEnd ≤ nominalEnd。
- 历史可重复读：评审时刻 T 满足 T ≤ start+Rw，故对任意 t < T，
  评审前后 t 是否落在 [start, effEnd) 内不变；新授权 start > t 不影响。
- 并发：Service 内一把互斥锁串行化所有操作，等价于某串行顺序；
  Access 只遍历该 principal 自己的授权切片（byRequester 索引），
  考察数与他人授权规模无关，用非导出计数器 accessChecks 断言。
- 评审唯一：已决（approvedAt/rejectedAt 已置）后再评审一律已决错误，
  已决先于逾期判断。

## 本地验证

```bash
export PATH=$PATH:/usr/local/go/bin
go vet ./... && gofmt -l .
go test -race -v ./policy/... ./ledger/... ./grant/...
```
