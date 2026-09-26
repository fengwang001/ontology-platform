# NOTES — counter-mode stream cipher (ontology-673)

推导：key=0x0123456789ABCDEF nonce=0x1，seed=splitmix64(key^nonce)=0xe821eebbc0778421，block0=splitmix64(seed)=0x8a236066f56b86f6（小端字节低→高 f6 86 6b f5 66 60 23 8a）。

| j | 明文 | 密钥流 | 密文 |
|---|---|---|---|
| 0 | 0x61 'a' | 0xf6 | 0x97 |
| 1 | 0x62 'b' | 0x86 | 0xe4 |
| 2 | 0x63 'c' | 0x6b | 0x08 |
| 3 | 0x64 'd' | 0xf5 | 0x91 |
| 4 | 0x65 'e' | 0x66 | 0x03 |
| 5 | 0x66 'f' | 0x60 | 0x06 |
| 6 | 0x67 'g' | 0x23 | 0x44 |
| 7 | 0x68 'h' | 0x8a | 0xe2 |

(甲) 大端取首字节=block0>>56=**0x8a（错）**；正确小端首字节=**0xf6**，8 个密钥流字节整体逆序。
(乙) 块号从 1 起：第 0 块错成 splitmix64(seed^1)=**0x84aede9228372122**，其首字节 **0x22（错）**；正确块 **0x8a236066f56b86f6**、首字节 **0xf6**。
(丙) C1^C2=(P1^K)^(P2^K)=**P1^P2**，密钥流抵消（two-time pad）：知一条明文即还原另一条，并泄露两明文相同位置/重复规律。处置：New 见已注册 (key,nonce) 整体拒绝（ErrNonceReuse），不发第二条密钥流。

不变量保证位置与钉住测试：

1. 加解密对称：XOR 自反，`stream.go` xorAt 同一路径（api.Encrypt=Decrypt）；`TestEncryptDecryptRoundTrip`。
2. 与朴素参照一致：`stream.go` blockAt=SplitMix64(seed^i) 与小端移位；`TestKeystreamMatchesNaiveReference`。
3. 随机访问一致：xorAt 按 pos/8 直接定位块，不预生成；`TestEncryptAtMatchesSequential`、`TestRandomAccessComputesOneBlock`（白盒读非导出计数，m=100…10000 恒 1 块）。
4. 失败不留痕：`api.go` New 先校验 key/nonce，再上锁查重，失败路径不写注册表；`TestRejectionLeavesNoTrace`（三类哨兵互不相同另见 `TestInvalidInputsSentinels`）。
