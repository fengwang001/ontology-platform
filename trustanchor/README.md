# RFC 5011 风格信任锚自动更新跟踪器

`trustanchor.Tracker` 根据已信任密钥签名的密钥集观测，跟踪密钥加入、恢复、缺失、吊销和封禁。

## 配置

构造函数：

```go
func New(h, r int64, kmax, requiredAppearances, quorum int, anchors ...[]byte) (*Tracker, error)
```

- `H`：加入保持期，范围 `0..10^9` 秒。
- `R`：`REVOKED` 与 `MISSING` 的保留期，范围 `1..10^9` 秒。
- `Kmax`：被跟踪密钥数上限，范围 `1..1000`。
- `M`：新密钥晋升所需连续出现次数，范围 `1..100`。
- `Q`：签名法定数，范围 `1..Kmax`。
- 初始信任锚数量必须在 `Q..Kmax` 内；每个标识必须是非空、互不相同的字节串。

任一配置不满足时，构造整体拒绝，不产生跟踪器。初始锚在全局时钟 `0` 时均为 `VALID`。

## 状态

- `ADDPEND`：待加入；`since` 是本轮连续出现第一次被接受观测的时刻，`count` 是连续出现次数。
- `VALID`：已信任；作为签名者时可计入法定数。
- `MISSING`：曾信任但在一次被接受观测中缺席；`since` 是开始缺失的时刻。
- `REVOKED`：已吊销；`at` 是吊销时刻，重复吊销不刷新。
- `BANNED`：永久封禁；保存在独立集合中，不占用 `Kmax`，无论以后是否出现或带吊销位都被忽略。
- `UNTRACKED`：未跟踪；没有 `since`、`at` 或计数。

`State(key)` 返回 `KeyState{Status, Since, At, Count}`。`Since` 用于 `ADDPEND` 和 `MISSING`，`At` 用于 `REVOKED` 与 `BANNED`。

## 观测接口

```go
func (t *Tracker) Observe(entries []Entry, signers [][]byte, now int64) error
```

- `entries` 长度为 `1..64`，每项包含非空 `Key` 和布尔吊销位；同一观测中的标识必须互不相同。
- `signers` 可为空，允许重复和未知标识；签名者列表先去重，再按观测开始前的状态计数。
- `now` 范围为 `0..10^15`；若小于上一次被接受观测的时间，为时钟回退。
- 拒绝在副本上判定；只有三阶段处理和后续检查全部成功后才提交，因此拒绝不会改变状态、时钟或封禁集合。

## 固定拒绝次序

每次观测只返回第一个失败：

1. 参数非法：`INVALID_ARGS`；配置非法在构造时返回 `INVALID_CONFIG`。
2. 时钟回退：`CLOCK_ROLLBACK`，先于签名者检查。
3. 签名者不可信：`UNTRUSTED_SIGNERS`；去重后，只有观测开始前为 `VALID` 的签名者计数，达到 `Q` 才通过。
4. 锁死：`DEADLOCK`；三阶段处理后 `VALID` 数量小于 `Q`。
5. 超限：`LIMIT_EXCEEDED`；三阶段处理后被跟踪密钥数大于 `Kmax`。

`RejectError` 的 `Reason` 可区分错误类别，`Index` 定位参数位置，`Detail` 定位字段，`Count/At/Since` 带出实际值、门槛值或时钟边界。

## 三阶段处理

所有阶段使用同一个观测时刻 `now`。

### 第一阶段：处理缺席

对每个被跟踪但不在本次密钥集中的密钥：

| 原状态 | 新状态 | 计时字段 |
| --- | --- | --- |
| `VALID` | `MISSING` | `since=now` |
| `ADDPEND` | `UNTRACKED` | 删除记录，连续计数和计时丢失 |
| `MISSING` | `MISSING` | 保持原 `since` |
| `REVOKED` | `REVOKED` | 保持原 `at` |

### 第二阶段：处理密钥集

`BANNED` 标识在最前面被整项忽略。

吊销位为真：

| 原状态 | 新状态 | 说明 |
| --- | --- | --- |
| `UNTRACKED` | `UNTRACKED` | 不开始跟踪 |
| `ADDPEND` | `UNTRACKED` | 连续计时与计数丢失 |
| `VALID` | `REVOKED` | `at=now` |
| `MISSING` | `REVOKED` | `at=now`，清除缺失起点 |
| `REVOKED` | `REVOKED` | 保持原 `at`，不刷新 |

吊销位为假：

| 原状态 | 新状态 | 说明 |
| --- | --- | --- |
| `UNTRACKED` | `ADDPEND` | `since=now, count=1` |
| `ADDPEND` | `ADDPEND` | `count++`，保持原 `since` |
| `VALID` | `VALID` | 不变 |
| `MISSING` | `VALID` | 恢复信任，清除 `since` |
| `REVOKED` | `REVOKED` | 不会因重新出现而信任 |

### 第三阶段：同刻推进

- `ADDPEND`：当 `now >= since+H` 且 `count >= M` 时晋升为 `VALID`。`H=0, M=1` 时，本阶段可晋升同一次新见到的密钥。
- `REVOKED`：当 `now >= at+R` 时移入 `BANNED`，保存其原 `at`，并从跟踪表删除。
- `MISSING`：当 `now >= since+R` 时变为 `UNTRACKED`，但不进入 `BANNED`。

第三阶段结束后，先检查 `VALID >= Q`，再检查被跟踪数 `<= Kmax`，所以锁死优先于超限。新晋升的 `VALID` 也参与锁死检查。签名法定数只看观测开始前，因此一个即将被吊销的 `VALID` 密钥可以签署并吊销自身。

## 安全不变量

- 所有操作由读写锁串行化；`Observe` 以副本计算并在成功后原子提交，`State` 看到一致快照。
- 只要构造成功，接受观测后 `VALID` 数量始终不小于 `Q`。
- 进入 `BANNED` 的标识永不重新跟踪，也不占用 `Kmax`。
- 同一轮 `ADDPEND` 连续出现期间，`since` 保持为第一次出现时刻；缺席或带吊销位会删除记录，下一次重新从 `count=1` 开始。
- 被拒绝的观测不修改任何状态、`since/at`、`count`、封禁集合、处理项计数器或最大已接受时钟。
- 非导出字段 `processed` 只累计接受观测实际处理的密钥项；每次增量不超过“观测开始前被跟踪密钥数 + 本次密钥集项数”，扫描封禁集合不计入。

## 本地验证

```bash
go test ./...
go test -race -v ./trustanchor
go test -v ./trustanchor -run TestRandomSequencesMatchNaiveSimulation
```

随机测试使用固定种子运行 2000 组观测序列，并把紧凑输入、时间、接受/拒绝输出、拒绝原因和接受后的时钟写入测试日志。测试中的朴素模拟器独立实现同一套转移规则，用于与生产实现逐项比对。
