# PTT Alertor (現代重構版)

PTT 文章即時追蹤機器人（Ptt Alertor）現代化復刻版本。

本版本去除了原版過期的 LINE Notify OAuth 流程、Facebook Messenger、AWS DynamoDB 與 Redis 複雜架構，全面改用 **LINE Messaging API** 與 **PostgreSQL (Supabase)**，並針對 **Render** 雲端平台進行輕量化與防休眠最佳化。

---

## 核心特色

1. **LINE Messaging API 原生整合**：全面採用 LINE 官方最新 Messaging API（Push Message 與 Reply Token），支援 1 對 1 及群組即時推播。
2. **單一 PostgreSQL 架構**：原生支援 Supabase / Neon 等雲端資料庫，程式啟動時自動完成 Schema 與索引建立（Auto Migration）。
3. **低記憶體高並行爬蟲**：以 Go 撰寫，執行時僅需約 20~30MB 記憶體，支援 `over18` 自動驗證、推文數換算（含「爆」與「XX」）及 Atom RSS 備援。
4. **Render Free Tier 友善**：提供 `/health` 健康檢查端點，配合外部定時 Ping（如 UptimeRobot），即可在 Render 免費層 24 小時不間斷運作。

---

## 指令一覽表

在 LINE 聊天室中傳送以下文字即可操作：

| 功能類別 | 指令格式 | 範例說明 |
| :--- | :--- | :--- |
| **一般指令** | `指令` 或 `help` | 顯示所有可用指令與格式說明 |
| | `清單` 或 `list` | 列出目前已設定的所有追蹤項目 |
| | `清除全部` 或 `clear` | 清空個人的所有追蹤設定 |
| **關鍵字追蹤** | `新增 看板 關鍵字` | `新增 gossiping,stock 台積電,輝達`（支援多看板、多關鍵字以逗號分隔） |
| | `刪除 看板 關鍵字` | `刪除 gossiping 台積電`（若關鍵字輸入 `*` 則刪除該板全部關鍵字） |
| **作者追蹤** | `新增作者 看板 作者` | `新增作者 stock ffaarr,obov` |
| | `刪除作者 看板 作者` | `刪除作者 stock ffaarr`（若作者輸入 `*` 則刪除該板全部作者） |
| **推文數追蹤** | `新增推文數 看板 門檻` | `新增推文數 beauty 50` 或 `新增推文數 gossiping 爆`（門檻為 0~100 或「爆」） |
| | `刪除推文數 看板` | `刪除推文數 beauty` |

---

## 快速部署指南 (Render + Supabase)

### 步驟一：取得 Supabase PostgreSQL 連線字串

1. 登入 [Supabase Dashboard](https://supabase.com/)。
2. 進入你的專案 -> 點擊左下角 **Project Settings** -> **Database**。
3. 在 **Connection parameters** / **Connection string** 中找到 **URI**：
   * **建議使用 Transaction Pooler**（Port 6543）：
     ```
     postgresql://postgres.[PROJECT_ID]:[YOUR_PASSWORD]@aws-0-ap-northeast-2.pooler.supabase.com:6543/postgres?sslmode=require
     ```
   * 請將 `[YOUR_PASSWORD]` 替換為你的 Supabase 資料庫密碼。
   * 資料表在程式首次啟動時會自動建立，無需手動執行 SQL。

### 步驟二：佈署至 Render

1. 將本專案 Push 至你的 GitHub 倉庫。
2. 登入 [Render Dashboard](https://dashboard.render.com/)，點擊 **New +** -> **Web Service**。
3. 選擇連接你剛剛 Push 的 GitHub 倉庫。
4. 設定參數：
   * **Name**: `ptt-alertor`
   * **Region**: `Singapore` (或離台灣最近的節點)
   * **Language**: `Docker`
   * **Instance Type**: `Free`
5. 在 **Environment Variables** 區塊填入以下環境變數：
   * `PORT`: `8080`
   * `CHECK_INTERVAL_SECONDS`: `15`
   * `CLEANUP_INTERVAL_HOURS`: `24`
   * `LINE_CHANNEL_SECRET`: 你的 LINE Channel Secret
   * `LINE_CHANNEL_ACCESS_TOKEN`: 你的 LINE Channel Access Token
   * `DATABASE_URL`: 步驟一取得的 Supabase 連線字串
6. 點擊 **Create Web Service** 開始構建。

### 步驟三：設定 LINE Developers Webhook URL

1. 部署完成後，Render 會分配一個公開網址，例如：`https://ptt-alertor-xxxx.onrender.com`。
2. 前往 [LINE Developers Console](https://developers.line.biz/) -> 選擇你的 Messaging API Channel。
3. 進入 **Messaging API** 分頁：
   * **Webhook URL**: 填入 `https://<你的-Render-網址>/line/callback`
   * 點擊 **Update**，並點擊 **Verify** 確認回傳 `Success`。
   * 將 **Use webhook** 開關切換為 **ON**。
   * 在 **LINE Official Account features** 中，將 **Auto-reply messages**（自動回應訊息）關閉，避免官方罐頭訊息干擾。

### 步驟四：防休眠設定（重要：讓 Free Tier 24 小時長駐）

Render 免費 Web Service 在 15 分鐘無連線時會自動休眠，導致爬蟲暫停。透過免費外部監控可保持常駐：

1. 註冊免費監控平台（例如 [UptimeRobot](https://uptimerobot.com/) 或 [cron-job.org](https://cron-job.org/)）。
2. 新增一個 Monitor：
   * **Monitor Type**: `HTTP(s)`
   * **URL**: `https://<你的-Render-網址>/health`
   * **Monitoring Interval**: 每 `5` 或 `10` 分鐘一次。
3. 如此即可確保服務永不休眠，爬蟲每 15 秒常態巡檢 PTT 最新文章。

---

## 本地開發與測試

若需在本地執行：

```bash
# 1. 複製環境設定檔
cp .env.example .env

# 2. 編輯 .env 填入實際憑證

# 3. 執行程式 (需已安裝 Go 1.23+)
go run main.go
```
