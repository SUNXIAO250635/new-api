<div align="center">

![new-api](/web/public/logo.png)

# New API

**An AI gateway, with billing and access control built for resale**

<p align="center">
  <a href="./README.zh_CN.md">简体中文</a> |
  <a href="./README.zh_TW.md">繁體中文</a> |
  <strong>English</strong> |
  <a href="./README.fr.md">Français</a> |
  <a href="./README.ja.md">日本語</a>
</p>

<p align="center">
  <strong>Fork of <a href="https://github.com/QuantumNous/new-api">QuantumNous/new-api</a></strong> ·
  <a href="#what-this-fork-adds">What this fork adds</a> ·
  <a href="#quick-start">Quick start</a> ·
  <a href="#upstream">Upstream</a>
</p>

</div>

---

## What this fork is

This is a fork of [new-api](https://github.com/QuantumNous/new-api), a self-hosted AI gateway that aggregates 40+ upstream model providers behind one OpenAI-compatible API.

Upstream already does model routing, access control, usage logging, and wallet billing. **This fork keeps all of that and adds the pieces needed to actually sell access**: subscription plans, welfare balance, a proper redemption-code system, and the accounting guarantees that go with them.

Everything below is what this fork adds or changes. For anything not listed here — protocols, channels, deployment, plugin system, console basics — see [upstream](#upstream); the behavior is the same.

---

<a id="what-this-fork-adds"></a>

## What this fork adds

### 1. Subscription plans

Upstream has a wallet. This fork adds a second funding source with its own lifecycle.

| Capability | Detail |
| --- | --- |
| Plan management | Create/edit/disable plans; price, duration, total quota, upgrade and downgrade groups |
| Quota reset | Reset used quota on a cycle: never / daily / weekly / monthly / custom seconds |
| Usage groups | Restrict a plan to specific routing groups; empty means all groups |
| Concurrency limit | Cap simultaneous requests per subscription (0 = unlimited) |
| Wallet overflow | Choose whether a plan may fall back to wallet balance when its quota runs out |
| Purchase limit | Max purchases per user per plan |
| Grant paths | Balance payment, Stripe, Creem, Epay, WaffoPancake, redemption code, or admin bind |
| Admin operations | Bind, invalidate, delete, reset by plan or by user |

Console: **Subscriptions** — one surface for both admins (plan create/edit, bind, invalidate, reset) and users (their own subscriptions, purchase, billing strategy).

### 2. Welfare balance

A separate, administrator-granted balance that sits alongside the wallet.

| Capability | Detail |
| --- | --- |
| Admin grant | Set any user's welfare balance and its expiry from the user edit dialog |
| Consumption order | Welfare is spent before wallet and subscription when it can cover the request |
| Expiry | Optional absolute expiry; expired balance is never charged |
| Version fencing | Every grant bumps a version. Reservations, settlement, and refunds are pinned to the version that funded them, so an administrator override mid-request cannot redirect quota to a different batch |

Console: **Users → Set Welfare Balance**.

### 3. Redemption codes

Upstream's redemption codes only add wallet quota. This fork turns them into a real distribution mechanism.

| Capability | Detail |
| --- | --- |
| Three benefit types | **Quota** (wallet top-up), **Welfare** (welfare balance with its own expiry), **Subscription** (grants a plan) |
| Batch generation | Create 1–100 codes in one operation, sharing a batch ID |
| Batch claim policy | By default each account may redeem only one code per batch; can be relaxed per batch |
| Batch delete | Delete selected codes, or sweep all used/expired/disabled ones |
| Export | Stream matching codes to a TXT file, one key per line |
| Expiry | Optional per-code expiry, independent of the benefit's own expiry |
| Audit | Every create, update, delete, and export is audit-logged |

Console: **Redemption Codes**. Self-service redemption stays in **Wallet**.

### 4. Billing strategy and funding fallback

Upstream picks a funding source implicitly. This fork lets each user choose, and makes the fallback explicit.

| Strategy | Behavior |
| --- | --- |
| Subscription First | Try subscription, fall back to wallet if allowed |
| Wallet First | Try wallet, fall back to subscription if allowed |
| Subscription Only | Never touch the wallet |
| Wallet Only | Never touch subscriptions |

Welfare balance is consumed automatically before either, when it can cover the request.

Console: **Wallet** — strategy selector and redemption entry.

### 5. Ordered token groups

Upstream tokens select a single group. This fork replaces that with an ordered group list.

- A token can name several groups, tried in order — the same channel-affinity and retry machinery decides the winner.
- Group choice is validated against what the user's own group may actually use.
- Ordered selection is also what subscription usage groups and plan restrictions are checked against.

Console: **Keys → Auto groups**.

### 6. Registration and logging controls

| Option | Effect |
| --- | --- |
| Restrict same-IP registration | One account per IP address. Enforced with a unique claim table plus per-database locking (advisory lock on PostgreSQL, locking read on MySQL, single-writer on SQLite) |
| Force IP logging | Overrides per-user preference: every usage and error log records the client IP |

Console: **System Settings → Basic Authentication**.

### 7. Accounting guarantees

The billing path was reworked to survive failure, not just the happy path.

| Mechanism | What it prevents |
| --- | --- |
| Durable refund records | A failed request writes a refund record before touching balances; a worker retries until it lands. Idempotent per request ID and per component (funding, token) |
| Bounded retries | A permanently impossible refund is parked as `failed` after 20 attempts instead of retrying forever |
| Settlement reconciliation | When a token adjustment fails after funding settled, the desired target is persisted and a worker converges it |
| Pre-consume restore | If persisting settled task markers fails, the session rolls back to its durable reservation so a later poll cannot charge the same delta twice |
| Version-fenced welfare | Reservations, settlement, and refunds all carry the grant version (see above) |

### 8. Security hardening

Fixes applied on top of upstream, each with a regression test:

| Fix | Detail |
| --- | --- |
| Welfare refund fencing | A welfare task without a recorded grant version is refused rather than refunded into whatever batch is currently active |
| Disabled plans unreachable | Redemption codes cannot grant a disabled plan; both code creation and redemption check `enabled` |
| Concurrency lease freshness | A subscription lease is only re-armed for a pre-consume record inside the lease TTL, so a replayed request ID cannot occupy a slot it no longer owns |
| Notification URL validation | Webhook, Bark, and Gotify URLs are validated against the SSRF fetch policy when saved, not only when delivered |
| Dependency CVEs | 9 vulnerabilities in `x/net`, `x/image`, `x/text` cleared; `govulncheck` reports zero |

### 9. Console changes

- **Users** — welfare balance field and the `Set Welfare Balance` action
- **Keys** — auto-group ordering editor
- **Wallet** — subscription plan cards, billing strategy selector, redemption entry
- **Subscriptions** — plan list, purchase, and self-management
- **Redemption Codes** — full management surface with bulk actions and export
- **System Settings → Basic Authentication** — the two IP controls above

---

<a id="quick-start"></a>

## Quick start

```bash
docker run --name new-api -d --restart unless-stopped \
  -p 127.0.0.1:3000:3000 \
  -e TZ=Asia/Shanghai \
  -v "$(pwd)/data:/data" \
  calciumion/new-api:latest
```

Open <http://localhost:3000> and complete the setup wizard.

Then, in order:

1. **Subscriptions** — create a plan, or skip this and use the wallet only
2. **Channels** — add an upstream key and run a channel test
3. **Redemption Codes** — generate codes for the benefit type you want
4. Hand a code to a user; they redeem it in **Wallet**

Configuration, storage, multi-node deployment, and the full variable reference are identical to upstream: see the [environment example](./.env.example) and [upstream docs](https://docs.newapi.ai/en/docs).

---

<a id="upstream"></a>

## Upstream

This fork tracks [QuantumNous/new-api](https://github.com/QuantumNous/new-api) and merges its releases.

| | |
| --- | --- |
| Upstream project | <https://github.com/QuantumNous/new-api> |
| Based on | [One API](https://github.com/songquanpeng/one-api) (MIT) |
| Documentation | <https://docs.newapi.ai/en/docs> |
| Protocols, channels, plugins | See upstream README and [RelayKit](./relaykit/README.md) |

**What comes from upstream unchanged:** model routing and channel management, the four text protocols and their conversions, image/audio/realtime endpoints, the JavaScript task plugin system, passkeys / TOTP / OAuth / OIDC, audit logging, i18n, the console shell, and the deployment story.

### Keeping in sync

```bash
git remote add upstream https://github.com/QuantumNous/new-api.git
git fetch upstream
git merge upstream/main
```

The fork's changes are concentrated in billing, redemption, subscription, and welfare code. Expect conflicts in `service/billing*.go`, `model/redemption.go`, `model/subscription.go`, `model/welfare_quota.go`, and the corresponding frontend features.

---

## License

GNU Affero General Public License v3.0. See [LICENSE](./LICENSE), [NOTICE](./NOTICE), and [THIRD-PARTY-LICENSES.md](./THIRD-PARTY-LICENSES.md).

This project is developed from [One API](https://github.com/songquanpeng/one-api) (MIT) via [new-api](https://github.com/QuantumNous/new-api).

Use it lawfully. Obtain upstream keys and model access through authorized means and comply with upstream terms and the laws of your jurisdiction. Operating a public AI service carries filing, content-safety, and real-name obligations in many jurisdictions.
