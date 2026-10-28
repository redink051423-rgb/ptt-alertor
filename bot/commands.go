package bot

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/ptt-alertor/ptt-alertor/crawler"
	"github.com/ptt-alertor/ptt-alertor/models"
	"github.com/ptt-alertor/ptt-alertor/storage"
)

var boardRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]{2,30}$`)

func isValidBoardName(name string) bool {
	return boardRegex.MatchString(strings.TrimSpace(name))
}

type QuotaGetter func(forceFresh bool) (QuotaInfo, error)

type CommandHandler struct {
	db          *storage.DB
	crawler     *crawler.Crawler
	quotaGetter QuotaGetter
}

func NewCommandHandler(db *storage.DB, c *crawler.Crawler) *CommandHandler {
	return &CommandHandler{
		db:      db,
		crawler: c,
	}
}

func (h *CommandHandler) SetQuotaGetter(qg QuotaGetter) {
	h.quotaGetter = qg
}

// Handle executes user command and returns reply text
func (h *CommandHandler) Handle(lineID, text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	parts := strings.Fields(text)
	cmd := strings.ToLower(parts[0])

	switch cmd {
	case "指令", "help", "?", "？":
		return h.getHelpText()
	case "額度", "quota":
		return h.handleQuota()
	case "清單", "list":
		return h.handleList(lineID)
	case "清除全部", "clear":
		return h.handleClearAll(lineID)
	case "新增", "add":
		return h.handleAddKeyword(lineID, parts)
	case "刪除", "del", "delete":
		return h.handleRemoveKeyword(lineID, parts)
	case "排除", "exclude":
		return h.handleAddExclude(lineID, parts)
	case "刪除排除":
		return h.handleRemoveExclude(lineID, parts)
	case "新增作者":
		return h.handleAddAuthor(lineID, parts)
	case "刪除作者":
		return h.handleRemoveAuthor(lineID, parts)
	case "新增推文數":
		return h.handleAddPushSum(lineID, parts)
	case "刪除推文數":
		return h.handleRemovePushSum(lineID, parts)
	}

	return "無此指令。請輸入「指令」查看可用功能與格式。"
}

func (h *CommandHandler) getHelpText() string {
	return `【PTT Alertor 指令列表】

[一般]
* 指令：查看功能清單
* 清單：查看目前追蹤設定
* 額度：查詢本月剩餘推播額度
* 清除全部：清空所有追蹤設定

[關鍵字追蹤]
* 新增 看板 關鍵字 (多個以逗號分隔)
* 刪除 看板 關鍵字 (* 代表刪除該板所有關鍵字)
範例：新增 gossiping,stock 台積電,輝達
範例：刪除 gossiping 台積電

[排除關鍵字 (負向過濾，省額度必備)]
* 排除 看板 關鍵字 (標題含此字不推播)
* 刪除排除 看板 關鍵字 (* 代表刪除該板所有排除詞)
範例：排除 macshop 徵求,換
範例：刪除排除 macshop 徵求
(亦支援新增時直接帶減號：新增 macshop 17 pro -徵求)

[作者追蹤]
* 新增作者 看板 作者 (多個以逗號分隔)
* 刪除作者 看板 作者 (* 代表刪除該板所有作者)
範例：新增作者 stock ffaarr,obov
範例：刪除作者 stock ffaarr

[推文數追蹤]
* 新增推文數 看板 門檻 (0-100 或 爆)
* 刪除推文數 看板
範例：新增推文數 beauty 50
範例：新增推文數 gossiping 爆`
}

func (h *CommandHandler) handleQuota() string {
	if h.quotaGetter == nil {
		return "暫時無法取得額度資訊。"
	}

	info, err := h.quotaGetter(true)
	if err != nil {
		return fmt.Sprintf("查詢額度失敗：%v", err)
	}

	if !info.Limited {
		return fmt.Sprintf("【LINE 推播配額報告】\n當前方案：無上限方案\n本月已累計發送：%d 則", info.Used)
	}

	return fmt.Sprintf(`【LINE 推播配額報告】
本月額度上限：%d 則
目前累計發送：%d 則
剩餘可用額度：%d 則
（每月 1 號由 LINE 官方自動歸零重置）`, info.Total, info.Used, info.Remaining)
}

func (h *CommandHandler) handleList(lineID string) string {
	subs, err := h.db.ListUserSubscriptions(lineID)
	if err != nil {
		return "查詢清單失敗，請稍後再試。"
	}
	if len(subs) == 0 {
		return "目前尚無任何追蹤設定。\n請輸入「指令」查看新增方式。"
	}

	type boardSub struct {
		Keywords []string
		Excludes []string
		Authors  []string
		PushSums []string
	}

	boardMap := make(map[string]*boardSub)
	for _, sub := range subs {
		b := strings.ToLower(sub.Board)
		if _, ok := boardMap[b]; !ok {
			boardMap[b] = &boardSub{}
		}
		switch sub.Type {
		case models.SubTypeKeyword:
			boardMap[b].Keywords = append(boardMap[b].Keywords, sub.Value)
		case models.SubTypeExcludeKeyword:
			boardMap[b].Excludes = append(boardMap[b].Excludes, sub.Value)
		case models.SubTypeAuthor:
			boardMap[b].Authors = append(boardMap[b].Authors, sub.Value)
		case models.SubTypePushSum:
			boardMap[b].PushSums = append(boardMap[b].PushSums, sub.Value)
		}
	}

	var boards []string
	for b := range boardMap {
		boards = append(boards, b)
	}
	sort.Strings(boards)

	var sb strings.Builder
	sb.WriteString("【目前追蹤清單】\n")
	for _, b := range boards {
		bs := boardMap[b]
		sb.WriteString(fmt.Sprintf("\n看板【%s】\n", b))
		if len(bs.Keywords) > 0 {
			sb.WriteString(fmt.Sprintf("• 關鍵字: %s\n", strings.Join(bs.Keywords, ", ")))
		}
		if len(bs.Excludes) > 0 {
			sb.WriteString(fmt.Sprintf("• 排除詞: %s\n", strings.Join(bs.Excludes, ", ")))
		}
		if len(bs.Authors) > 0 {
			sb.WriteString(fmt.Sprintf("• 作者: %s\n", strings.Join(bs.Authors, ", ")))
		}
		if len(bs.PushSums) > 0 {
			sb.WriteString(fmt.Sprintf("• 推文數: %s\n", strings.Join(bs.PushSums, ", ")))
		}
	}

	return strings.TrimSpace(sb.String())
}

func (h *CommandHandler) handleClearAll(lineID string) string {
	if err := h.db.RemoveAllSubscriptions(lineID); err != nil {
		return "清除失敗，請稍後再試。"
	}
	return "已清除您的所有追蹤設定。"
}

func splitTokens(s string) []string {
	s = strings.ReplaceAll(s, "，", ",")
	raw := strings.Split(s, ",")
	var result []string
	for _, r := range raw {
		r = strings.TrimSpace(r)
		if r != "" {
			result = append(result, r)
		}
	}
	return result
}

func (h *CommandHandler) handleAddKeyword(lineID string, parts []string) string {
	if len(parts) < 3 {
		return "格式錯誤。\n正確範例：新增 gossiping,stock 台積電,輝達"
	}

	boards := splitTokens(parts[1])
	keywords := splitTokens(strings.Join(parts[2:], " "))

	if len(boards) == 0 || len(keywords) == 0 {
		return "格式錯誤：看板名稱與關鍵字不可為空。"
	}

	for _, b := range boards {
		if !isValidBoardName(b) {
			return fmt.Sprintf("看板名稱格式不正確：[%s]（僅能包含英數字與底線，長度 2~30 字元）。", b)
		}
	}

	var posAdded, negAdded int
	for _, b := range boards {
		bLower := strings.ToLower(b)
		for _, kw := range keywords {
			if strings.HasPrefix(kw, "-") {
				exKw := strings.TrimSpace(strings.TrimPrefix(kw, "-"))
				if exKw != "" {
					if err := h.db.AddSubscription(lineID, bLower, models.SubTypeExcludeKeyword, exKw); err == nil {
						negAdded++
					}
				}
			} else {
				if err := h.db.AddSubscription(lineID, bLower, models.SubTypeKeyword, kw); err == nil {
					posAdded++
				}
			}
		}
	}

	if negAdded > 0 {
		return fmt.Sprintf("新增設定成功！已追蹤 %d 筆關鍵字，已設定 %d 筆排除詞。", posAdded, negAdded)
	}
	return fmt.Sprintf("新增關鍵字成功！已追蹤 %d 筆規則。", posAdded)
}

func (h *CommandHandler) handleAddExclude(lineID string, parts []string) string {
	if len(parts) < 3 {
		return "格式錯誤。\n正確範例：排除 macshop 徵求,換"
	}

	boards := splitTokens(parts[1])
	keywords := splitTokens(strings.Join(parts[2:], " "))

	if len(boards) == 0 || len(keywords) == 0 {
		return "格式錯誤：看板名稱與排除關鍵字不可為空。"
	}

	for _, b := range boards {
		if !isValidBoardName(b) {
			return fmt.Sprintf("看板名稱格式不正確：[%s]（僅能包含英數字與底線，長度 2~30 字元）。", b)
		}
	}

	var addedCount int
	for _, b := range boards {
		bLower := strings.ToLower(b)
		for _, kw := range keywords {
			kwClean := strings.TrimSpace(strings.TrimPrefix(kw, "-"))
			if kwClean != "" {
				if err := h.db.AddSubscription(lineID, bLower, models.SubTypeExcludeKeyword, kwClean); err == nil {
					addedCount++
				}
			}
		}
	}

	return fmt.Sprintf("設定排除關鍵字成功！已新增 %d 筆排除規則。", addedCount)
}

func (h *CommandHandler) handleRemoveExclude(lineID string, parts []string) string {
	if len(parts) < 3 {
		return "格式錯誤。\n正確範例：刪除排除 macshop 徵求 (或使用 * 刪除全部)"
	}

	boards := splitTokens(parts[1])
	keywords := splitTokens(strings.Join(parts[2:], " "))

	var removedTotal int64
	for _, b := range boards {
		bLower := strings.ToLower(b)
		for _, kw := range keywords {
			kwClean := strings.TrimSpace(strings.TrimPrefix(kw, "-"))
			n, _ := h.db.RemoveSubscription(lineID, bLower, models.SubTypeExcludeKeyword, kwClean)
			removedTotal += n
		}
	}

	return fmt.Sprintf("刪除排除關鍵字完成，共移除 %d 筆設定。", removedTotal)
}

func (h *CommandHandler) handleRemoveKeyword(lineID string, parts []string) string {
	if len(parts) < 3 {
		return "格式錯誤。\n正確範例：刪除 gossiping 台積電 (或使用 * 刪除全部)"
	}

	boards := splitTokens(parts[1])
	keywords := splitTokens(strings.Join(parts[2:], " "))

	var removedTotal int64
	for _, b := range boards {
		bLower := strings.ToLower(b)
		for _, kw := range keywords {
			n, _ := h.db.RemoveSubscription(lineID, bLower, models.SubTypeKeyword, kw)
			removedTotal += n
		}
	}

	return fmt.Sprintf("刪除關鍵字完成，共移除 %d 筆設定。", removedTotal)
}

func (h *CommandHandler) handleAddAuthor(lineID string, parts []string) string {
	if len(parts) < 3 {
		return "格式錯誤。\n正確範例：新增作者 stock ffaarr,obov"
	}

	boards := splitTokens(parts[1])
	authors := splitTokens(strings.Join(parts[2:], " "))

	if len(boards) == 0 || len(authors) == 0 {
		return "格式錯誤：看板名稱與作者帳號不可為空。"
	}

	for _, b := range boards {
		if !isValidBoardName(b) {
			return fmt.Sprintf("看板名稱格式不正確：[%s]（僅能包含英數字與底線，長度 2~30 字元）。", b)
		}
	}

	var addedCount int
	for _, b := range boards {
		bLower := strings.ToLower(b)
		for _, a := range authors {
			if err := h.db.AddSubscription(lineID, bLower, models.SubTypeAuthor, a); err == nil {
				addedCount++
			}
		}
	}

	return fmt.Sprintf("新增作者成功！已追蹤 %d 筆作者規則。", addedCount)
}

func (h *CommandHandler) handleRemoveAuthor(lineID string, parts []string) string {
	if len(parts) < 3 {
		return "格式錯誤。\n正確範例：刪除作者 stock ffaarr (或使用 * 刪除全部)"
	}

	boards := splitTokens(parts[1])
	authors := splitTokens(strings.Join(parts[2:], " "))

	var removedTotal int64
	for _, b := range boards {
		bLower := strings.ToLower(b)
		for _, a := range authors {
			n, _ := h.db.RemoveSubscription(lineID, bLower, models.SubTypeAuthor, a)
			removedTotal += n
		}
	}

	return fmt.Sprintf("刪除作者完成，共移除 %d 筆設定。", removedTotal)
}

func (h *CommandHandler) handleAddPushSum(lineID string, parts []string) string {
	if len(parts) < 3 {
		return "格式錯誤。\n正確範例：新增推文數 beauty 50 (或 爆)"
	}

	boards := splitTokens(parts[1])
	valStr := strings.TrimSpace(parts[2])

	// Validate threshold
	threshold := valStr
	if valStr != "爆" && valStr != "XX" {
		num, err := strconv.Atoi(valStr)
		if err != nil || num < 0 || num > 100 {
			return "推文數門檻需為 0 至 100 之數字，或「爆」。"
		}
		threshold = strconv.Itoa(num)
	}

	for _, b := range boards {
		if !isValidBoardName(b) {
			return fmt.Sprintf("看板名稱格式不正確：[%s]（僅能包含英數字與底線，長度 2~30 字元）。", b)
		}
	}

	var addedCount int
	for _, b := range boards {
		bLower := strings.ToLower(b)
		if err := h.db.AddSubscription(lineID, bLower, models.SubTypePushSum, threshold); err == nil {
			addedCount++
		}
	}

	return fmt.Sprintf("新增推文數門檻 [%s] 成功，共設定 %d 個看板。", threshold, addedCount)
}

func (h *CommandHandler) handleRemovePushSum(lineID string, parts []string) string {
	if len(parts) < 2 {
		return "格式錯誤。\n正確範例：刪除推文數 beauty"
	}

	boards := splitTokens(parts[1])
	var removedTotal int64
	for _, b := range boards {
		bLower := strings.ToLower(b)
		n, _ := h.db.RemoveSubscription(lineID, bLower, models.SubTypePushSum, "*")
		removedTotal += n
	}

	return fmt.Sprintf("刪除推文數門檻完成，共移除 %d 個看板設定。", removedTotal)
}
