<div align="center">

![new-api](/web/public/logo.png)

# New API

**一个 AI 网关，计费与权限控制为转售而生**

<p align="center">
  <a href="./README.zh_TW.md">繁體中文</a> |
  <strong>简体中文</strong> |
  <a href="./README.md">English</a> |
  <a href="./README.fr.md">Français</a> |
  <a href="./README.ja.md">日本語</a>
</p>

<p align="center">
  <strong><a href="https://github.com/QuantumNous/new-api">QuantumNous/new-api</a> 的分支</strong> ·
  <a href="#what-this-fork-adds">本分支新增了什么</a> ·
  <a href="#quick-start">快速开始</a> ·
  <a href="#upstream">上游项目</a>
</p>

</div>

---

## 这是什么

本项目是 [new-api](https://github.com/QuantumNous/new-api) 的分支。new-api 是一个自托管的 AI 网关，把 40 多个上游模型服务聚合到一套 OpenAI 兼容接口之后。

上游已经提供了模型路由、访问控制、用量日志和钱包计费。**本分支保留这些能力，并补上了真正对外售卖所需要的东西**：订阅套餐、福利余额、一套完整的兑换码体系，以及与之配套的计费保障。

下面列出的都是本分支新增或修改的内容。未提及的部分——协议、渠道、部署、插件系统、控制台基础操作——与上游完全一致，见<a href="#upstream">上游项目</a>。

---

<a id="what-this-fork-adds"></a>

## 本分支新增的能力

### 1. 订阅套餐

上游只有钱包，本分支增加了第二种资金来源，并有完整的生命周期。

| 能力 | 说明 |
| --- | --- |
| 套餐管理 | 创建、编辑、停用套餐；价格、时长、总额度、升级/降级分组 |
| 配额重置 | 按周期重置已用额度：从不 / 每日 / 每周 / 每月 / 自定义秒数 |
| 用量分组 | 限制套餐只能用于指定路由分组；留空表示不限制 |
| 并发限制 | 限制单个订阅的同时请求数（0 为不限制） |
| 钱包溢出 | 套餐额度用尽后是否允许回落到钱包余额 |
| 购买上限 | 每个用户可购买同一套餐的次数上限 |
| 发放路径 | 余额支付、Stripe、Creem、易支付、WaffoPancake、兑换码、管理员直接发放 |
| 管理操作 | 绑定、作废、删除、按套餐或按用户重置 |

控制台：**Subscriptions（订阅管理）**——管理员和用户共用同一个页面，管理员在此创建/编辑套餐、绑定、作废、重置，用户在此查看自己的订阅、购买、设置扣费策略。

### 2. 福利余额

一种独立于钱包、由管理员发放的余额。

| 能力 | 说明 |
| --- | --- |
| 管理员发放 | 在用户编辑弹窗中设置任意用户的福利余额及其过期时间 |
| 消耗顺序 | 福利余额足以覆盖请求时，优先于钱包和订阅消耗 |
| 过期机制 | 可设置绝对过期时间，过期余额绝不会被扣 |
| 版本围栏 | 每次发放都会递增版本号。预扣、结算、退款全部绑定到为该请求出资的那个版本，因此请求进行中管理员修改配额不会把额度记到另一个批次上 |

控制台：**Users（用户管理）→ Set Welfare Balance（设置福利余额）**。

### 3. 兑换码

上游的兑换码只能给钱包充值。本分支把它做成了真正的分发机制。

| 能力 | 说明 |
| --- | --- |
| 三种权益类型 | **Quota（额度）** 充值到钱包、**Welfare（福利）** 发放福利余额并可单独设置过期时间、**Subscription（订阅）** 直接授予套餐 |
| 批量生成 | 一次生成 1–100 个兑换码，共用同一个批次 ID |
| 批次领取策略 | 默认每个账号在同一批次只能领取一次，可按批次放开 |
| 批量删除 | 删除选中的兑换码，或一键清理已用/已过期/已停用的 |
| 导出 | 按当前筛选条件流式导出为 TXT 文件，每行一个兑换码 |
| 过期时间 | 可设置独立的兑换码过期时间，与权益自身的过期时间无关 |
| 审计 | 创建、修改、删除、导出全部记入审计日志 |

控制台：**Redemption Codes（兑换码管理）**。用户自助兑换在 **Wallet（钱包）**。

### 4. 扣费策略与资金来源回落

上游隐式选择资金来源，本分支让每个用户自己选，并把回落规则显式化。

| 策略 | 行为 |
| --- | --- |
| Subscription First（订阅优先） | 先试订阅，策略允许时回落到钱包 |
| Wallet First（钱包优先） | 先试钱包，策略允许时回落到订阅 |
| Subscription Only（仅订阅） | 不动钱包 |
| Wallet Only（仅钱包） | 不动订阅 |

福利余额在两个之前自动消耗——只要它足以覆盖请求。

控制台：**Wallet（钱包）**——策略选择器和兑换入口都在这里。

### 5. 令牌有序分组

上游的令牌只能选一个分组，本分支换成了有序分组列表。

- 一个令牌可以指定多个分组，按顺序尝试，由原有的渠道亲和与重试机制决定最终落在哪个。
- 分组选择会对照用户自身分组实际可用的范围做校验。
- 有序分组同时也是订阅用量分组和套餐限制的比对基准。

控制台：**Keys（令牌管理）→ Auto groups（自动分组）**。

### 6. 注册与日志管控

| 选项 | 效果 |
| --- | --- |
| Restrict same-IP registration（同 IP 限注册） | 一个 IP 地址只能注册一个账号。通过唯一声明表加按数据库的锁实现：PostgreSQL 用咨询锁，MySQL 用锁读，SQLite 靠单写串行 |
| Force IP logging（强制 IP 日志） | 覆盖用户个人偏好，所有用量日志和错误日志都记录客户端 IP |

控制台：**System Settings → Basic Authentication（基础认证）**。

### 7. 计费保障

计费链路按"出错也能对账"重做过，不只是跑通 happy path。

| 机制 | 防止什么 |
| --- | --- |
| 持久化退款记录 | 请求失败时先落一条退款记录再动余额，由后台worker重试到落地。按请求 ID 和按组件（资金、令牌）双重幂等 |
| 有限重试 | 永远不可能成功的退款在 20 次尝试后置为 `failed` 停放，不会每 30 秒无限重试 |
| 结算对账 | 令牌调整在资金已提交后失败时，把目标值持久化，由 worker 收敛到位 |
| 预扣回滚 | 任务结算标记落库失败时，会话回滚到持久化的预扣水位，之后的轮询不会把同一笔差额扣两次 |
| 福利版本围栏 | 预扣、结算、退款都携带发放版本号（见上） |

### 8. 安全加固

在上游基础上的修复，每一项都有回归测试：

| 修复 | 说明 |
| --- | --- |
| 福利退款围栏 | 没有记录发放版本的福利任务直接拒绝退款，而不是把额度退回当前生效的批次 |
| 停用套餐不可达 | 兑换码不能授予已停用的套餐，创建和兑换两处都检查 `enabled` |
| 并发租约新鲜度 | 订阅租约只为租约 TTL 内的预扣记录重新武装，重放的请求 ID 无法占用它已不再持有的并发槽 |
| 通知 URL 校验 | Webhook、Bark、Gotify 的地址在保存时就按 SSRF 策略校验，而不是只等到投递时兜底 |
| 依赖 CVE | 清除 `x/net`、`x/image`、`x/text` 共 9 个漏洞，`govulncheck` 报告为零 |

### 9. 控制台变化

- **Users（用户管理）** — 福利余额字段和 `Set Welfare Balance` 操作
- **Keys（令牌管理）** — 自动分组排序编辑器
- **Wallet（钱包）** — 订阅套餐卡片、扣费策略选择器、兑换入口
- **Subscriptions（订阅管理）** — 套餐列表、购买、自助管理
- **Redemption Codes（兑换码管理）** — 完整的管理界面，含批量操作和导出
- **System Settings → Basic Authentication（基础认证）** — 上述两个 IP 相关开关

---

<a id="quick-start"></a>

## 快速开始

```bash
docker run --name new-api -d --restart unless-stopped \
  -p 127.0.0.1:3000:3000 \
  -e TZ=Asia/Shanghai \
  -v "$(pwd)/data:/data" \
  calciumion/new-api:latest
```

打开 <http://localhost:3000>，完成初始化向导。

然后按顺序：

1. **Subscriptions（订阅管理）** — 创建套餐；如果只用钱包可以跳过
2. **Channels（渠道）** — 添加上游密钥并测试渠道
3. **Redemption Codes（兑换码管理）** — 按你想要的权益类型生成兑换码
4. 把兑换码发给用户，由他在 **Wallet（钱包）** 里兑换

配置、存储、多节点部署和完整的环境变量说明与上游一致，见[环境变量示例](./.env.example)和[上游文档](https://docs.newapi.ai/en/docs)。

---

<a id="upstream"></a>

## 上游项目

本分支跟踪 [QuantumNous/new-api](https://github.com/QuantumNous/new-api) 并合并其版本。

| | |
| --- | --- |
| 上游项目 | <https://github.com/QuantumNous/new-api> |
| 项目源头 | [One API](https://github.com/songquanpeng/one-api)（MIT） |
| 文档 | <https://docs.newapi.ai/en/docs> |
| 协议、渠道、插件 | 见上游 README 与 [RelayKit](./relaykit/README.md) |

**以下能力直接来自上游，未做修改**：模型路由与渠道管理、四种文本协议及其转换、图像/音频/实时接口、JavaScript 任务插件系统、Passkey / TOTP / OAuth / OIDC、审计日志、国际化、控制台框架、部署方式。

### 与上游保持同步

```bash
git remote add upstream https://github.com/QuantumNous/new-api.git
git fetch upstream
git merge upstream/main
```

本分支的改动集中在计费、兑换码、订阅、福利相关代码。预期冲突会出现在 `service/billing*.go`、`model/redemption.go`、`model/subscription.go`、`model/welfare_quota.go` 以及对应的前端模块。

---

## 许可证

GNU Affero 通用公共许可证 v3.0。见 [LICENSE](./LICENSE)、[NOTICE](./NOTICE) 和 [THIRD-PARTY-LICENSES.md](./THIRD-PARTY-LICENSES.md)。

本项目基于 [One API](https://github.com/songquanpeng/one-api)（MIT）经 [new-api](https://github.com/QuantumNous/new-api) 开发而来。

请合法使用。通过授权渠道获取上游密钥与模型访问权限，并遵守上游服务条款及所在司法辖区的法律法规。在多个辖区，对外提供公开 AI 服务还涉及备案、内容安全、实名等义务。
