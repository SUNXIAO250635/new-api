<div align="center">

![new-api](/web/public/logo.png)

# New API

**一個 AI 閘道，計費與權限控制為轉售而生**

<p align="center">
  <strong>繁體中文</strong> |
  <a href="./README.zh_CN.md">简体中文</a> |
  <a href="./README.md">English</a> |
  <a href="./README.fr.md">Français</a> |
  <a href="./README.ja.md">日本語</a>
</p>

<p align="center">
  <strong><a href="https://github.com/QuantumNous/new-api">QuantumNous/new-api</a> 的分支</strong> ·
  <a href="#what-this-fork-adds">本分支新增了什麼</a> ·
  <a href="#quick-start">快速開始</a> ·
  <a href="#upstream">上游專案</a>
</p>

</div>

---

## 這是什麼

本專案是 [new-api](https://github.com/QuantumNous/new-api) 的分支。new-api 是一個自架 AI 閘道，把 40 多個上游模型服務聚合到一套 OpenAI 相容介面之後。

上游已經提供模型路由、存取控制、用量紀錄與錢包計費。**本分支保留這些能力，並補上真正對外售賣所需要的東西**：訂閱方案、福利餘額、一套完整的兌換碼體系，以及與之配套的計費保障。

下面列出的是本分支新增或修改的內容。未提及的部分——協定、渠道、部署、外掛系統、控制台基礎操作——與上游完全一致，見<a href="#upstream">上游專案</a>。

---

<a id="what-this-fork-adds"></a>

## 本分支新增的能力

### 1. 訂閱方案

上游只有錢包，本分支增加第二種資金來源，並有完整的生命週期。

| 能力 | 說明 |
| --- | --- |
| 方案管理 | 建立、編輯、停用方案；價格、時長、總額度、升級／降級分組 |
| 配額重設 | 依週期重設已用額度：永不 / 每日 / 每週 / 每月 / 自訂秒數 |
| 用量分組 | 限制方案只能用於指定路由分組；留空表示不限制 |
| 並行限制 | 限制單一訂閱的同時請求數（0 為不限制） |
| 錢包溢出 | 方案額度用盡後是否允許回落到錢包餘額 |
| 購買上限 | 每個使用者可購買同一方案的次數上限 |
| 發放路徑 | 餘額支付、Stripe、Creem、易支付、WaffoPancake、兌換碼、管理員直接發放 |
| 管理操作 | 綁定、作廢、刪除、依方案或依使用者重設 |

控制台：**Subscriptions（訂閱管理）**——管理員與使用者共用同一個頁面，管理員在此建立／編輯方案、綁定、作廢、重設，使用者在此查看自己的訂閱、購買、設定扣費策略。

### 2. 福利餘額

一種獨立於錢包、由管理員發放的餘額。

| 能力 | 說明 |
| --- | --- |
| 管理員發放 | 在使用者編輯彈窗中設定任意使用者的福利餘額與其過期時間 |
| 消耗順序 | 福利餘額足以覆蓋請求時，優先於錢包與訂閱消耗 |
| 過期機制 | 可設定絕對過期時間，過期餘額絕不會被扣 |
| 版本圍欄 | 每次發放都會遞增版本號。預扣、結算、退款全部綁定到為該請求出資的那個版本，因此請求進行中管理員修改配額不會把額度記到另一個批次上 |

控制台：**Users（使用者管理）→ Set Welfare Balance（設定福利餘額）**。

### 3. 兌換碼

上游的兌換碼只能幫錢包充值。本分支把它做成了真正的分發機制。

| 能力 | 說明 |
| --- | --- |
| 三種權益類型 | **Quota（額度）** 充值到錢包、**Welfare（福利）** 發放福利餘額並可單獨設定過期時間、**Subscription（訂閱）** 直接授予方案 |
| 批次產生 | 一次產生 1–100 個兌換碼，共用同一個批次 ID |
| 批次領取策略 | 預設每個帳號在同一批次只能領取一次，可按批次放開 |
| 批次刪除 | 刪除選取的兌換碼，或一鍵清理已用／已過期／已停用的 |
| 匯出 | 依目前篩選條件串流匯出為 TXT 檔案，每行一個兌換碼 |
| 過期時間 | 可設定獨立的兌換碼過期時間，與權益自身的過期時間無關 |
| 稽核 | 建立、修改、刪除、匯出全部記入稽核紀錄 |

控制台：**Redemption Codes（兌換碼管理）**。使用者自助兌換在 **Wallet（錢包）**。

### 4. 扣費策略與資金來源回落

上游隱性選擇資金來源，本分支讓每個使用者自己選，並把回落規則明確化。

| 策略 | 行為 |
| --- | --- |
| Subscription First（訂閱優先） | 先試訂閱，策略允許時回落到錢包 |
| Wallet First（錢包優先） | 先試錢包，策略允許時回落到訂閱 |
| Subscription Only（僅訂閱） | 不動錢包 |
| Wallet Only（僅錢包） | 不動訂閱 |

福利餘額在兩者之前自動消耗——只要它足以覆蓋請求。

控制台：**Wallet（錢包）**——策略選擇器與兌換入口都在這裡。

### 5. 令牌有序分組

上游的令牌只能選一個分組，本分支換成了有序分組清單。

- 一個令牌可以指定多個分組，依序嘗試，由原有的渠道親和與重試機制決定最終落在哪個。
- 分組選擇會對照使用者自身分組實際可用的範圍做驗證。
- 有序分組同時也是訂閱用量分組與方案限制的比對基準。

控制台：**Keys（令牌管理）→ Auto groups（自動分組）**。

### 6. 註冊與紀錄管控

| 選項 | 效果 |
| --- | --- |
| Restrict same-IP registration（同 IP 限註冊） | 一個 IP 位址只能註冊一個帳號。透過唯一宣告表加上依資料庫的鎖實作：PostgreSQL 用諮詢鎖，MySQL 用鎖定讀取，SQLite 靠單寫串行 |
| Force IP logging（強制 IP 紀錄） | 覆寫使用者個人偏好，所有用量紀錄與錯誤紀錄都記錄客戶端 IP |

控制台：**System Settings → Basic Authentication（基礎認證）**。

### 7. 計費保障

計費鏈路依「出錯也能對帳」重做過，不只是跑通 happy path。

| 機制 | 防止什麼 |
| --- | --- |
| 持久化退款紀錄 | 請求失敗時先寫一筆退款紀錄再動餘額，由背景 worker 重試到落地。依請求 ID 與依元件（資金、令牌）雙重冪等 |
| 有限重試 | 永遠不可能成功的退款在 20 次嘗試後設為 `failed` 停放，不會每 30 秒無限重試 |
| 結算對帳 | 令牌調整在資金已提交後失敗時，把目標值持久化，由 worker 收斂到位 |
| 預扣回滾 | 任務結算標記寫入失敗時，工作階段回滾到持久化的預扣水位，之後的輪詢不會把同一筆差額扣兩次 |
| 福利版本圍欄 | 預扣、結算、退款都攜帶發放版本號（見上） |

### 8. 安全強化

在上游基礎上的修復，每一項都有迴歸測試：

| 修復 | 說明 |
| --- | --- |
| 福利退款圍欄 | 沒有記錄發放版本的福利任務直接拒絕退款，而不是把額度退回當前生效的批次 |
| 停用方案不可達 | 兌換碼不能授予已停用的方案，建立與兌換兩處都檢查 `enabled` |
| 並行租約新鮮度 | 訂閱租約只為租約 TTL 內的預扣記錄重新武装，重放的請求 ID 無法佔用它已不再持有的並行槽 |
| 通知 URL 驗證 | Webhook、Bark、Gotify 的位址在儲存時就依 SSRF 策略驗證，而不是只等到投遞時兜底 |
| 依賴 CVE | 清除 `x/net`、`x/image`、`x/text` 共 9 個漏洞，`govulncheck` 報告為零 |

### 9. 控制台變化

- **Users（使用者管理）** — 福利餘額欄位與 `Set Welfare Balance` 操作
- **Keys（令牌管理）** — 自動分組排序編輯器
- **Wallet（錢包）** — 訂閱方案卡片、扣費策略選擇器、兌換入口
- **Subscriptions（訂閱管理）** — 方案清單、購買、自助管理
- **Redemption Codes（兌換碼管理）** — 完整的管理介面，含批次操作與匯出
- **System Settings → Basic Authentication（基礎認證）** — 上述兩個 IP 相關開關

---

<a id="quick-start"></a>

## 快速開始

```bash
docker run --name new-api -d --restart unless-stopped \
  -p 127.0.0.1:3000:3000 \
  -e TZ=Asia/Shanghai \
  -v "$(pwd)/data:/data" \
  calciumion/new-api:latest
```

開啟 <http://localhost:3000>，完成初始化精靈。

然後依序：

1. **Subscriptions（訂閱管理）** — 建立方案；如果只用錢包可以跳過
2. **Channels（渠道）** — 新增上游金鑰並測試渠道
3. **Redemption Codes（兌換碼管理）** — 依你想要的權益類型產生兌換碼
4. 把兌換碼發給使用者，由他在 **Wallet（錢包）** 裡兌換

設定、儲存、多節點部署與完整的環境變數說明與上游一致，見[環境變數範例](./.env.example)與[上游文件](https://docs.newapi.ai/en/docs)。

---

<a id="upstream"></a>

## 上游專案

本分支追蹤 [QuantumNous/new-api](https://github.com/QuantumNous/new-api) 並合併其版本。

| | |
| --- | --- |
| 上游專案 | <https://github.com/QuantumNous/new-api> |
| 專案源頭 | [One API](https://github.com/songquanpeng/one-api)（MIT） |
| 文件 | <https://docs.newapi.ai/en/docs> |
| 協定、渠道、外掛 | 見上游 README 與 [RelayKit](./relaykit/README.md) |

**以下能力直接來自上游，未做修改**：模型路由與渠道管理、四種文字協定及其轉換、影像／音訊／即時介面、JavaScript 任務外掛系統、Passkey / TOTP / OAuth / OIDC、稽核紀錄、國際化、控制台框架、部署方式。

### 與上游保持同步

```bash
git remote add upstream https://github.com/QuantumNous/new-api.git
git fetch upstream
git merge upstream/main
```

本分支的改動集中在計費、兌換碼、訂閱、福利相關程式碼。預期衝突會出現在 `service/billing*.go`、`model/redemption.go`、`model/subscription.go`、`model/welfare_quota.go` 以及對應的前端模組。

---

## 授權條款

GNU Affero 通用公共授權條款 v3.0。見 [LICENSE](./LICENSE)、[NOTICE](./NOTICE) 與 [THIRD-PARTY-LICENSES.md](./THIRD-PARTY-LICENSES.md)。

本專案基於 [One API](https://github.com/songquanpeng/one-api)（MIT）經 [new-api](https://github.com/QuantumNous/new-api) 開發而來。

請合法使用。透過授權管道取得上游金鑰與模型存取權限，並遵守上游服務條款及所在司法管轄區的法律法規。在多個轄區，對外提供公開 AI 服務還涉及備案、內容安全、實名等義務。
