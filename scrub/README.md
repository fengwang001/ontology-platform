# scrub：多副本块存储巡检与修复仲裁

后台校验巡检、版本仲裁、修复写入、告警与到期调度的参考实现。
设计取舍与复杂度证明见 `../docs/scrub-design.md`。

## 包结构

- `scrub/arbitrate.go`：纯仲裁函数 `Arbitrate`（无副作用，开销只随副本数）。
- `scrub/dueindex.go`：`(到期时刻, 块号)` 的 treap 到期索引。
- `scrub/store.go`：`Store` 服务（时钟、间隔、告警、事件注入、巡检、到期选取）。
- `scrub/naivemodel/`：独立编写的朴素参考模型（线性扫描到期），供随机对照。

## 终局分类

| 分类 | 含义 | 是否改副本 | 是否记巡检时刻 | 是否告警 |
| --- | --- | --- | --- | --- |
| `no-repair-needed` | 已与权威一致 | 否 | 是 | 否 |
| `repaired` | 需修复且全部成功 | 是 | 是 | 否 |
| `partial-repair` | 存在写失败（含全部失败） | 成功的改、失败的不动 | 是 | 否 |
| `no-available-source` | 没有自洽副本 | 否 | 是 | 是 |
| `committed-data-lost` | 最大自洽版本 < 已提交版本 | 否 | 是 | 是 |
| `version-conflict` | 最大自洽版本摘要不一致 | 否 | 是 | 是 |

拒绝类（不改变任何状态、不留痕）按次序为：参数非法、时钟回退、
块不存在、过于频繁。

## API 速览

```go
s := scrub.NewStore()
s.CreateBlock(at, blockID, replicas, minInterval) // 2..5 个不同节点
s.Write(at, blockID, version, digest, nodes)      // 新版本写到若干节点
s.Rot(at, blockID, node, corruptDigest)           // 模拟位腐
s.Discard(at, blockID, node)                      // 丢弃副本（到 0 块消失）
res, err := s.Patrol(at, blockID, writeFailNodes) // 巡检+修复仲裁
ids, err := s.Due(at, limit)                      // 到期块（只读）
alerts := s.Alerts()                              // 追加式、不合并
```

`PatrolResult` 同时给出 `CommittedVersion`、权威版本/摘要、每个修复目标的
节点与原因（位腐 / 版本落后）、成功节点与失败节点，可精确复现一次巡检。

## 验证

```bash
export PATH=$PATH:/usr/local/go/bin GOCACHE=/tmp/gocache
go test -race -v ./scrub/...
```

随机对照日志：`$TMPDIR/scrub-diff-logs/seed*.log`（或 `SCRUB_DIFF_LOG` 指定），
逐行记录每条操作的输入、两侧输出与判定依据。
