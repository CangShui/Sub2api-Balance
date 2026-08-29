package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
)

var version = "0.2.0"

type options struct {
	refreshSeconds int
}

type settings struct {
	Endpoint string
	APIKey   string
}

type periodStats struct {
	Used       float64
	Limit      float64
	HasLimit   bool
	Requests   int64
	TotalToken int64
}

type usageReport struct {
	Valid        bool
	Mode         string
	PlanName     string
	Unit         string
	Balance      *float64
	Remaining    *float64
	Daily        periodStats
	Weekly       periodStats
	Monthly      periodStats
	BalanceStats periodStats
	DailyRecords []dailyRecord
	ExpiresAt    string
}

type dailyRecord struct {
	Date         string  `json:"date"`
	Requests     int64   `json:"requests"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	TotalTokens  int64   `json:"total_tokens"`
	Cost         float64 `json:"cost"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	opts, err := parseOptions(args)
	if errors.Is(err, errHelp) {
		printUsage(stdout)
		return 0
	}
	if err != nil {
		fmt.Fprintf(stderr, "参数错误：%v\n\n", err)
		printUsage(stderr)
		return 2
	}

	settings, err := loadSettings()
	if err != nil {
		fmt.Fprintf(stderr, "配置错误：%v\n", err)
		return 2
	}

	if opts.refreshSeconds > 0 {
		return runRefresh(settings, opts.refreshSeconds, stdout, stderr)
	}
	return runOnce(settings, stdout, stderr)
}

func runOnce(settings settings, stdout, stderr io.Writer) int {
	report, err := queryReport(settings)
	if err != nil {
		fmt.Fprintf(stderr, "查询失败：%v\n", err)
		return 1
	}
	printDashboard(stdout, settings.Endpoint, report, 0, time.Time{})
	return 0
}

func runRefresh(settings settings, interval int, stdout, stderr io.Writer) int {
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	defer signal.Stop(interrupt)

	for {
		report, err := queryReport(settings)
		clearScreen(stdout)
		if err != nil {
			fmt.Fprintf(stdout, "sub2api 自动刷新\n\n查询失败：%v\n", err)
			fmt.Fprintf(stdout, "将在 %d 秒后重试，按 Ctrl+C 退出。\n", interval)
		} else {
			printDashboard(stdout, settings.Endpoint, report, interval, time.Now())
		}

		select {
		case <-ticker.C:
		case <-interrupt:
			fmt.Fprintln(stdout, "\n已退出自动刷新。")
			return 0
		}
	}
}

func queryReport(settings settings) (usageReport, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	body, err := fetchUsage(ctx, settings.Endpoint, settings.APIKey)
	if err != nil {
		return usageReport{}, err
	}
	return parseUsage(body)
}

var errHelp = errors.New("help")

func parseOptions(args []string) (options, error) {
	if len(args) == 0 {
		return options{}, nil
	}
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		return options{}, errHelp
	}
	if args[0] != "-f" && !strings.HasPrefix(args[0], "-f=") && !strings.HasPrefix(args[0], "-f") {
		return options{}, errors.New("只支持 sub2api 或 sub2api -f [秒数]")
	}
	if len(args) > 2 {
		return options{}, errors.New("只支持 sub2api 或 sub2api -f [秒数]")
	}

	value := "10"
	switch {
	case args[0] == "-f" && len(args) == 2:
		value = args[1]
	case strings.HasPrefix(args[0], "-f=") && len(args) == 1:
		value = strings.TrimPrefix(args[0], "-f=")
	case strings.HasPrefix(args[0], "-f") && args[0] != "-f" && len(args) == 1:
		value = strings.TrimPrefix(args[0], "-f")
	case args[0] == "-f" && len(args) == 1:
	default:
		return options{}, errors.New("刷新间隔必须是正整数秒数")
	}

	seconds, err := strconv.Atoi(value)
	if err != nil || seconds <= 0 {
		return options{}, errors.New("刷新间隔必须是正整数秒数")
	}
	return options{refreshSeconds: seconds}, nil
}

func loadSettings() (settings, error) {
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return settings{}, fmt.Errorf("无法确定用户目录：%w", err)
		}
		home = filepath.Join(userHome, ".codex")
	}

	configPath := filepath.Join(home, "config.toml")
	authPath := filepath.Join(home, "auth.json")

	endpoint, err := readBaseURL(configPath)
	if err != nil {
		return settings{}, err
	}
	endpoint, err = usageEndpoint(endpoint)
	if err != nil {
		return settings{}, err
	}

	apiKey, err := readAPIKey(authPath)
	if err != nil {
		return settings{}, err
	}
	if apiKey == "" {
		return settings{}, errors.New("没有找到 API key，请检查当前 Codex 目录中的 auth.json")
	}

	return settings{Endpoint: endpoint, APIKey: apiKey}, nil
}

func readBaseURL(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("读取 Codex 配置 %s 失败：%w", path, err)
	}

	provider := ""
	sections := map[string]map[string]string{}
	currentSection := ""
	for _, rawLine := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(stripTOMLComment(rawLine))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			currentSection = strings.TrimSpace(line[1 : len(line)-1])
			if _, ok := sections[currentSection]; !ok {
				sections[currentSection] = map[string]string{}
			}
			continue
		}
		key, value, ok := splitTOMLAssignment(line)
		if !ok {
			continue
		}
		if currentSection == "" && key == "model_provider" {
			provider = parseTOMLString(value)
		}
		if _, ok := sections[currentSection]; !ok {
			sections[currentSection] = map[string]string{}
		}
		sections[currentSection][key] = parseTOMLString(value)
	}

	if provider != "" {
		if values := sections["model_providers."+provider]; values != nil && values["base_url"] != "" {
			return values["base_url"], nil
		}
	}
	for section, values := range sections {
		if strings.HasPrefix(section, "model_providers.") && values["base_url"] != "" {
			return values["base_url"], nil
		}
	}
	return "", fmt.Errorf("配置 %s 中没有找到 model_providers.*.base_url", path)
}

func readAPIKey(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("读取 Codex 认证文件 %s 失败：%w", path, err)
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(data, &values); err != nil {
		return "", fmt.Errorf("解析认证文件 %s 失败：%w", path, err)
	}
	for _, wanted := range []string{"OPENAI_API_KEY", "api_key", "token", "access_token"} {
		for key, raw := range values {
			if normalizeKey(key) != normalizeKey(wanted) {
				continue
			}
			var value string
			if err := json.Unmarshal(raw, &value); err == nil {
				return strings.TrimSpace(value), nil
			}
		}
	}
	return "", fmt.Errorf("认证文件 %s 中没有找到 OPENAI_API_KEY", path)
}

func usageEndpoint(base string) (string, error) {
	base = strings.TrimSpace(base)
	if base == "" {
		return "", errors.New("端点为空，请检查当前 Codex 目录中的 config.toml")
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("无效端点 %q", base)
	}
	path := strings.TrimRight(u.Path, "/")
	switch {
	case strings.HasSuffix(path, "/usage"):
		// 已经是查询接口地址。
	case strings.HasSuffix(path, "/v1"):
		path += "/usage"
	default:
		path += "/v1/usage"
	}
	u.Path = path
	u.RawPath = ""
	return u.String(), nil
}

func fetchUsage(ctx context.Context, endpoint, apiKey string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败：%w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("User-Agent", "sub2api-cli/"+version)

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("连接端点失败：%w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("读取响应失败：%w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(string(body))
		if len(message) > 1000 {
			message = message[:1000] + "..."
		}
		if message == "" {
			message = resp.Status
		}
		return nil, fmt.Errorf("HTTP %s：%s", resp.Status, message)
	}
	return body, nil
}

func parseUsage(data []byte) (usageReport, error) {
	var root any
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	if err := decoder.Decode(&root); err != nil {
		return usageReport{}, err
	}
	if _, ok := root.(map[string]any); !ok {
		return usageReport{}, errors.New("接口响应不是 JSON 对象")
	}

	report := usageReport{}
	report.Valid = boolValue(findPath(root, []string{"isValid"}, []string{"valid"}))
	report.Mode = stringValue(findPath(root, []string{"mode"}))
	report.PlanName = stringValue(findPath(root, []string{"planName"}, []string{"plan_name"}))
	report.Unit = stringValue(findPath(root, []string{"unit"}))
	report.Remaining = firstNumber(root,
		[]string{"remaining"},
		[]string{"balance"},
		[]string{"user", "remaining"},
		[]string{"user", "balance"},
		[]string{"usage", "remaining"},
	)

	subscription := findPath(root, []string{"subscription"})
	quota := findPath(root, []string{"quota"})
	rateLimits := findPath(root, []string{"rate_limits"}, []string{"rateLimits"})
	report.Balance = firstNumber(root,
		[]string{"balance"},
		[]string{"user", "balance"},
		[]string{"wallet", "balance"},
	)
	if report.Balance == nil && subscription == nil && quota == nil && !hasItems(rateLimits) {
		// 没有订阅、总额度或速率限制时，remaining 才表示钱包余额。
		report.Balance = report.Remaining
	}
	if report.Balance == nil {
		zero := 0.0
		report.Balance = &zero
	}
	report.ExpiresAt = stringValue(findPath(subscription, []string{"expiresAt"}, []string{"expires_at"}))
	report.Daily = periodFrom(subscription, "daily")
	report.Weekly = periodFrom(subscription, "weekly")
	report.Monthly = periodFrom(subscription, "monthly")

	usage := findPath(root, []string{"usage"})
	if report.Daily.Used == 0 && report.Daily.Limit == 0 {
		report.Daily = periodFrom(usage, "today")
	}
	if report.Monthly.Used == 0 && report.Monthly.Limit == 0 {
		report.Monthly = periodFrom(usage, "total")
	}
	report.DailyRecords = parseDailyRecords(root)
	if report.Daily.Requests == 0 && len(report.DailyRecords) > 0 {
		report.Daily.Requests = report.DailyRecords[len(report.DailyRecords)-1].Requests
		report.Daily.TotalToken = report.DailyRecords[len(report.DailyRecords)-1].TotalTokens
	}
	if report.Weekly.Used == 0 && len(report.DailyRecords) > 0 {
		report.Weekly = sumRecentRecords(report.DailyRecords, 7)
	}
	report.BalanceStats = parseBalanceStats(root, subscription, quota, usage, report.DailyRecords, report.Balance, report.Remaining)
	return report, nil
}

func hasItems(value any) bool {
	switch value := value.(type) {
	case []any:
		return len(value) > 0
	case map[string]any:
		return len(value) > 0
	default:
		return value != nil
	}
}

func periodFrom(value any, period string) periodStats {
	if value == nil {
		return periodStats{}
	}
	limitValue := findPath(value, []string{period + "_limit_usd"}, []string{period + "LimitUsd"}, []string{"limit", period}, []string{period, "limit"})
	return periodStats{
		Used:       numberValue(findPath(value, []string{period + "_usage_usd"}, []string{period + "UsageUsd"}, []string{"usage", period, "actual_cost"}, []string{"usage", period, "actualCost"}, []string{"usage", period, "cost"}, []string{period, "actual_cost"}, []string{period, "actualCost"}, []string{period, "cost"})),
		Limit:      numberValue(limitValue),
		HasLimit:   limitValue != nil,
		Requests:   intValue(findPath(value, []string{"requests"})),
		TotalToken: intValue(findPath(value, []string{"total_tokens"}, []string{"totalTokens"})),
	}
}

func parseDailyRecords(root any) []dailyRecord {
	value := findPath(root, []string{"daily_usage"}, []string{"dailyUsage"})
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	records := make([]dailyRecord, 0, len(items))
	for _, item := range items {
		if _, ok := item.(map[string]any); !ok {
			continue
		}
		records = append(records, dailyRecord{
			Date:         stringValue(findPath(item, []string{"date"})),
			Requests:     intValue(findPath(item, []string{"requests"})),
			InputTokens:  intValue(findPath(item, []string{"input_tokens"}, []string{"inputTokens"})),
			OutputTokens: intValue(findPath(item, []string{"output_tokens"}, []string{"outputTokens"})),
			TotalTokens:  intValue(findPath(item, []string{"total_tokens"}, []string{"totalTokens"})),
			Cost:         numberValue(findPath(item, []string{"actual_cost"}, []string{"actualCost"}, []string{"cost"})),
		})
	}
	return records
}

func sumRecentRecords(records []dailyRecord, days int) periodStats {
	cutoff := time.Now().AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	var result periodStats
	for _, record := range records {
		if record.Date != "" && record.Date < cutoff {
			continue
		}
		result.Used += record.Cost
		result.Requests += record.Requests
		result.TotalToken += record.TotalTokens
	}
	return result
}

func sumAllRecords(records []dailyRecord) periodStats {
	var result periodStats
	for _, record := range records {
		result.Used += record.Cost
		result.Requests += record.Requests
		result.TotalToken += record.TotalTokens
	}
	return result
}

func parseBalanceStats(root, subscription, quota, usage any, dailyRecords []dailyRecord, balance, remaining *float64) periodStats {
	var stats periodStats

	subTotal := periodFrom(subscription, "total")
	if subTotal.HasLimit || subTotal.Used > 0 {
		stats = subTotal
	} else {
		subMonthly := periodFrom(subscription, "monthly")
		if subMonthly.HasLimit || subMonthly.Used > 0 {
			stats = subMonthly
		}
	}

	if quota != nil {
		if limitVal := firstNumber(quota, []string{"total"}, []string{"total_quota"}, []string{"limit"}, []string{"total_usd"}); limitVal != nil && !stats.HasLimit {
			stats.Limit = *limitVal
			stats.HasLimit = true
		}
		if usedVal := firstNumber(quota, []string{"used"}, []string{"used_quota"}, []string{"usage"}, []string{"used_usd"}); usedVal != nil && stats.Used == 0 {
			stats.Used = *usedVal
		}
		if stats.Requests == 0 {
			stats.Requests = intValue(findPath(quota, []string{"requests"}))
		}
		if stats.TotalToken == 0 {
			stats.TotalToken = intValue(findPath(quota, []string{"total_tokens"}, []string{"totalTokens"}))
		}
	}

	if !stats.HasLimit {
		if limitVal := firstNumber(root, []string{"total_quota"}, []string{"total_limit_usd"}, []string{"totalLimitUsd"}, []string{"total_limit"}); limitVal != nil {
			stats.Limit = *limitVal
			stats.HasLimit = true
		}
	}
	if stats.Used == 0 {
		if usedVal := firstNumber(root, []string{"used_quota"}, []string{"total_usage_usd"}, []string{"totalUsageUsd"}); usedVal != nil {
			stats.Used = *usedVal
		}
	}

	if usage != nil {
		usageTotal := periodFrom(usage, "total")
		if stats.Used == 0 && usageTotal.Used > 0 {
			stats.Used = usageTotal.Used
		}
		if stats.Requests == 0 && usageTotal.Requests > 0 {
			stats.Requests = usageTotal.Requests
		}
		if stats.TotalToken == 0 && usageTotal.TotalToken > 0 {
			stats.TotalToken = usageTotal.TotalToken
		}
	}

	if len(dailyRecords) > 0 {
		allStats := sumAllRecords(dailyRecords)
		if stats.Used == 0 && allStats.Used > 0 {
			stats.Used = allStats.Used
		}
		if stats.Requests == 0 && allStats.Requests > 0 {
			stats.Requests = allStats.Requests
		}
		if stats.TotalToken == 0 && allStats.TotalToken > 0 {
			stats.TotalToken = allStats.TotalToken
		}
	}

	if balance != nil {
		bal := *balance
		if !stats.HasLimit {
			stats.Limit = stats.Used + bal
			stats.HasLimit = true
		} else if stats.Used == 0 && bal < stats.Limit {
			stats.Used = stats.Limit - bal
		}
	} else if remaining != nil && !stats.HasLimit {
		rem := *remaining
		stats.Limit = stats.Used + rem
		stats.HasLimit = true
	}

	return stats
}

func findPath(value any, paths ...[]string) any {
	for _, path := range paths {
		current := value
		found := true
		for _, name := range path {
			object, ok := current.(map[string]any)
			if !ok {
				found = false
				break
			}
			current, ok = mapValue(object, name)
			if !ok {
				found = false
				break
			}
		}
		if found {
			return current
		}
	}
	return nil
}

func mapValue(values map[string]any, wanted string) (any, bool) {
	for key, value := range values {
		if normalizeKey(key) == normalizeKey(wanted) {
			return value, true
		}
	}
	return nil, false
}

func normalizeKey(value string) string {
	value = strings.ToLower(value)
	value = strings.ReplaceAll(value, "_", "")
	value = strings.ReplaceAll(value, "-", "")
	return value
}

func firstNumber(root any, paths ...[]string) *float64 {
	for _, path := range paths {
		value := numberValue(findPath(root, path))
		if findPath(root, path) != nil {
			return &value
		}
	}
	return nil
}

func numberValue(value any) float64 {
	switch value := value.(type) {
	case json.Number:
		result, _ := value.Float64()
		return result
	case float64:
		return value
	case float32:
		return float64(value)
	case int:
		return float64(value)
	case int64:
		return float64(value)
	case string:
		result, _ := strconv.ParseFloat(strings.TrimSpace(value), 64)
		return result
	default:
		return 0
	}
}

func intValue(value any) int64 {
	return int64(numberValue(value))
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if result, ok := value.(string); ok {
		return result
	}
	return fmt.Sprint(value)
}

func boolValue(value any) bool {
	if result, ok := value.(bool); ok {
		return result
	}
	return strings.EqualFold(stringValue(value), "true")
}

func printDashboard(out io.Writer, endpoint string, report usageReport, refreshSeconds int, refreshedAt time.Time) {
	width := terminalWidth()
	balance := "接口未返回"
	if report.Balance != nil {
		balance = formatMoneyCompact(*report.Balance, report.Unit)
	}
	plan := report.PlanName
	if plan == "" {
		plan = "接口未返回"
	}
	rows := []string{
		"端点      " + endpoint,
		"余额      " + balance,
		"套餐      " + plan,
		"订阅到期  " + formatExpiry(report.ExpiresAt),
	}
	if !report.Valid {
		rows = append(rows, "状态      认证或订阅状态异常")
	}
	printBox(out, width, rows...)
	printUsageBar(out, width, "余额", report.BalanceStats, report.Unit)
	printUsageBar(out, width, "日限", report.Daily, report.Unit)
	printUsageBar(out, width, "周限", report.Weekly, report.Unit)

	if refreshSeconds > 0 && !refreshedAt.IsZero() {
		fmt.Fprintf(out, "\n%s\n", fitDisplay(fmt.Sprintf("最后刷新：%s  |  每 %d 秒刷新  |  Ctrl+C 退出", refreshedAt.Format("2006-01-02 15:04:05"), refreshSeconds), width))
	}
}

func printUsageBar(out io.Writer, width int, title string, period periodStats, unit string) {
	barWidth := width - 2
	if barWidth < 20 {
		barWidth = 20
	}
	percent := usagePercent(period)
	filled := int(float64(barWidth)*percent/100 + 0.5)
	if filled > barWidth {
		filled = barWidth
	}
	if filled < 0 {
		filled = 0
	}
	bar := strings.Repeat("█", filled) + strings.Repeat(" ", barWidth-filled)
	limit := "未设置"
	remaining := "未计算"
	if period.HasLimit || period.Limit > 0 {
		limit = formatMoneyCompact(period.Limit, unit)
		left := period.Limit - period.Used
		if left < 0 {
			left = 0
		}
		remaining = formatMoneyCompact(left, unit)
	}

	fmt.Fprintf(out, "\n%s\n", title)
	fmt.Fprintf(out, "┌%s┐\n", strings.Repeat("─", barWidth))
	fmt.Fprintf(out, "│%s│\n", bar)
	fmt.Fprintf(out, "└%s┘\n", strings.Repeat("─", barWidth))
	info := fmt.Sprintf("已用：%s    限额：%s    剩余：%s    使用率：%.0f%%", formatMoneyCompact(period.Used, unit), limit, remaining, percent)
	fmt.Fprintln(out, fitDisplay(info, width))
}

func usagePercent(period periodStats) float64 {
	if period.Limit <= 0 {
		return 0
	}
	percent := period.Used / period.Limit * 100
	if percent < 0 {
		return 0
	}
	if percent > 100 {
		return 100
	}
	return percent
}

func formatMoneyCompact(value float64, unit string) string {
	if unit == "" {
		unit = "USD"
	}
	return fmt.Sprintf("%.2f%s", value, unit)
}

func formatExpiry(value string) string {
	if value == "" {
		return "接口未返回"
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return value
	}
	shanghai := time.FixedZone("Asia/Shanghai", 8*60*60)
	return parsed.In(shanghai).Format("2006-01-02 15:04:05")
}

func printBox(out io.Writer, width int, rows ...string) {
	inner := width - 2
	if inner < 20 {
		inner = 20
	}
	border := strings.Repeat("─", inner)
	fmt.Fprintf(out, "┌%s┐\n", border)
	for _, row := range rows {
		content := fitDisplay(row, inner-2)
		fmt.Fprintf(out, "│ %s%s │\n", content, strings.Repeat(" ", inner-2-displayWidth(content)))
	}
	fmt.Fprintf(out, "└%s┘\n", border)
}

func terminalWidth() int {
	width := 96
	if value, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && value > 0 {
		width = value
	}
	if width < 70 {
		return 70
	}
	if width > 120 {
		return 120
	}
	return width
}

func displayWidth(value string) int {
	width := 0
	for _, r := range value {
		switch {
		case r == '\t':
			width += 4
		case unicode.Is(unicode.Han, r), unicode.Is(unicode.Hangul, r), unicode.Is(unicode.Hiragana, r), unicode.Is(unicode.Katakana, r):
			width += 2
		default:
			width++
		}
	}
	return width
}

func fitDisplay(value string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if displayWidth(value) <= maxWidth {
		return value
	}
	if maxWidth <= 3 {
		return "..."[:maxWidth]
	}
	result := ""
	for _, r := range value {
		candidate := result + string(r)
		if displayWidth(candidate)+3 > maxWidth {
			break
		}
		result = candidate
	}
	return result + "..."
}

func clearScreen(out io.Writer) {
	io.WriteString(out, "\x1b[2J\x1b[H")
}

func printUsage(out io.Writer) {
	io.WriteString(out, `sub2api - Sub2API 用量查询工具

用法：
  sub2api                 查询一次余额和日/周用量
  sub2api -f              每 10 秒自动刷新
  sub2api -f 30           每 30 秒自动刷新

默认配置：
  Windows: %USERPROFILE%\.codex
  Linux:   $HOME/.codex
  也支持环境变量 CODEX_HOME。

按 Ctrl+C 可退出自动刷新。`)
}

func splitTOMLAssignment(line string) (string, string, bool) {
	quoted := false
	quote := byte(0)
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '\'', '"':
			if !quoted {
				quoted = true
				quote = line[i]
			} else if quote == line[i] && (i == 0 || line[i-1] != '\\') {
				quoted = false
			}
		case '=':
			if !quoted {
				key := strings.TrimSpace(line[:i])
				if key == "" {
					return "", "", false
				}
				return key, strings.TrimSpace(line[i+1:]), true
			}
		}
	}
	return "", "", false
}

func parseTOMLString(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		if result, err := strconv.Unquote(value); err == nil {
			return result
		}
	}
	if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
		return value[1 : len(value)-1]
	}
	return value
}

func stripTOMLComment(line string) string {
	quoted := false
	quote := byte(0)
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '\'', '"':
			if !quoted {
				quoted = true
				quote = line[i]
			} else if quote == line[i] && (i == 0 || line[i-1] != '\\') {
				quoted = false
			}
		case '#':
			if !quoted {
				return line[:i]
			}
		}
	}
	return line
}
