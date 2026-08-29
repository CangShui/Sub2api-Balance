package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUsageEndpoint(t *testing.T) {
	tests := map[string]string{
		"https://example.com":           "https://example.com/v1/usage",
		"https://example.com/v1":        "https://example.com/v1/usage",
		"https://example.com/v1/":       "https://example.com/v1/usage",
		"https://example.com/api/v1":    "https://example.com/api/v1/usage",
		"https://example.com/v1/usage":  "https://example.com/v1/usage",
		"https://example.com/v1/usage/": "https://example.com/v1/usage",
	}
	for input, expected := range tests {
		actual, err := usageEndpoint(input)
		if err != nil {
			t.Fatalf("usageEndpoint(%q): %v", input, err)
		}
		if actual != expected {
			t.Errorf("usageEndpoint(%q) = %q, want %q", input, actual, expected)
		}
	}
}

func TestReadCodexConfigAndAuth(t *testing.T) {
	dir := t.TempDir()
	config := `[model_providers]

[model_providers.custom]
base_url = "https://sub2api.example/v1"
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := readBaseURL(filepath.Join(dir, "config.toml")); err != nil || got != "https://sub2api.example/v1" {
		t.Fatalf("readBaseURL() = %q, %v", got, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"OPENAI_API_KEY":"test-key-placeholder"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := readAPIKey(filepath.Join(dir, "auth.json")); err != nil || got != "test-key-placeholder" {
		t.Fatalf("readAPIKey() = %q, %v", got, err)
	}
}

func TestParseUsageResponse(t *testing.T) {
	data := []byte(`{
  "daily_usage": [{"date":"2026-08-22","requests":3,"total_tokens":1200,"cost":2.5}],
  "isValid": true,
  "mode": "unrestricted",
  "planName": "OpenAI - 360",
  "remaining": 77.5,
  "unit": "USD",
  "subscription": {
    "daily_limit_usd": 100,
    "daily_usage_usd": 22.5,
    "weekly_limit_usd": 360,
    "weekly_usage_usd": 22.5,
    "expires_at": "2026-09-21T15:07:07+08:00"
  }
}`)
	report, err := parseUsage(data)
	if err != nil {
		t.Fatal(err)
	}
	if report.Remaining == nil || *report.Remaining != 77.5 {
		t.Fatalf("remaining = %v", report.Remaining)
	}
	if report.Balance == nil || *report.Balance != 0 {
		t.Fatalf("subscription balance = %v, want 0", report.Balance)
	}
	if report.Daily.Used != 22.5 || report.Daily.Limit != 100 {
		t.Fatalf("daily = %+v", report.Daily)
	}
	if report.Weekly.Used != 22.5 || report.Weekly.Limit != 360 {
		t.Fatalf("weekly = %+v", report.Weekly)
	}
	if len(report.DailyRecords) != 1 || report.DailyRecords[0].Requests != 3 {
		t.Fatalf("daily records = %+v", report.DailyRecords)
	}
}

func TestWalletBalanceUsesRemaining(t *testing.T) {
	report, err := parseUsage([]byte(`{"isValid":true,"remaining":12.5,"unit":"USD","usage":{"total":{"cost":2.5}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if report.Balance == nil || *report.Balance != 12.5 {
		t.Fatalf("wallet balance = %v, want 12.5", report.Balance)
	}
}

func TestParseRefreshOptions(t *testing.T) {
	tests := []struct {
		args     []string
		expected int
	}{
		{args: nil, expected: 0},
		{args: []string{"-f"}, expected: 10},
		{args: []string{"-f", "5"}, expected: 5},
		{args: []string{"-f=20"}, expected: 20},
		{args: []string{"-f30"}, expected: 30},
	}
	for _, test := range tests {
		opts, err := parseOptions(test.args)
		if err != nil {
			t.Fatalf("parseOptions(%v): %v", test.args, err)
		}
		if opts.refreshSeconds != test.expected {
			t.Errorf("parseOptions(%v).refreshSeconds = %d, want %d", test.args, opts.refreshSeconds, test.expected)
		}
	}
	for _, args := range [][]string{{"-f", "0"}, {"-f", "abc"}, {"daily"}, {"-f", "5", "extra"}} {
		if _, err := parseOptions(args); err == nil {
			t.Errorf("parseOptions(%v) expected error", args)
		}
	}
}

func TestParseBalanceStats(t *testing.T) {
	// 1. Total quota subscription test
	totalQuotaData := []byte(`{
  "isValid": true,
  "planName": "Total Quota Plan",
  "unit": "USD",
  "subscription": {
    "total_limit_usd": 100,
    "total_usage_usd": 25
  }
}`)
	report1, err := parseUsage(totalQuotaData)
	if err != nil {
		t.Fatal(err)
	}
	if !report1.BalanceStats.HasLimit || report1.BalanceStats.Limit != 100 || report1.BalanceStats.Used != 25 {
		t.Fatalf("total quota BalanceStats = %+v, want Limit=100 Used=25", report1.BalanceStats)
	}

	// 2. Wallet balance test
	walletData := []byte(`{
  "isValid": true,
  "balance": 80.0,
  "unit": "USD",
  "usage": {
    "total": {
      "cost": 20.0
    }
  }
}`)
	report2, err := parseUsage(walletData)
	if err != nil {
		t.Fatal(err)
	}
	if !report2.BalanceStats.HasLimit || report2.BalanceStats.Limit != 100 || report2.BalanceStats.Used != 20 {
		t.Fatalf("wallet BalanceStats = %+v, want Limit=100 Used=20", report2.BalanceStats)
	}

	// 3. Quota object test
	quotaData := []byte(`{
  "isValid": true,
  "quota": {
    "total": 50.0,
    "used": 10.0
  }
}`)
	report3, err := parseUsage(quotaData)
	if err != nil {
		t.Fatal(err)
	}
	if !report3.BalanceStats.HasLimit || report3.BalanceStats.Limit != 50 || report3.BalanceStats.Used != 10 {
		t.Fatalf("quota BalanceStats = %+v, want Limit=50 Used=10", report3.BalanceStats)
	}
}

func TestPrintDashboardWithBalanceBar(t *testing.T) {
	report := usageReport{
		Valid:    true,
		PlanName: "Test Plan",
		Unit:     "USD",
		Daily: periodStats{
			Used:     10,
			Limit:    100,
			HasLimit: true,
		},
		Weekly: periodStats{
			Used:     50,
			Limit:    300,
			HasLimit: true,
		},
		BalanceStats: periodStats{
			Used:     20,
			Limit:    100,
			HasLimit: true,
		},
	}
	bal := 80.0
	report.Balance = &bal

	var buf strings.Builder
	printDashboard(&buf, "https://example.com/v1", report, 0, time.Time{})
	out := buf.String()

	idxBalance := strings.Index(out, "余额\n┌")
	idxDaily := strings.Index(out, "日限\n┌")
	idxWeekly := strings.Index(out, "周限\n┌")

	if idxBalance == -1 || idxDaily == -1 || idxWeekly == -1 {
		t.Fatalf("missing bar in output:\n%s", out)
	}
	if !(idxBalance < idxDaily && idxDaily < idxWeekly) {
		t.Errorf("expected order: 余额 < 日限 < 周限, got balance=%d, daily=%d, weekly=%d", idxBalance, idxDaily, idxWeekly)
	}
}

func TestRealSub2apiWalletUsage(t *testing.T) {
	data := []byte(`{"balance":7.28784931,"daily_usage":[{"date":"2026-07-31","requests":278,"input_tokens":1888927,"output_tokens":95224,"cache_read_tokens":38381184,"cache_write_tokens":0,"total_tokens":40365335,"cost":31.4771612,"actual_cost":3.14771612},{"date":"2026-08-14","requests":398,"input_tokens":2396216,"output_tokens":259001,"cache_read_tokens":46824192,"cache_write_tokens":0,"total_tokens":49479409,"cost":42.8556008,"actual_cost":4.28556008},{"date":"2026-08-15","requests":460,"input_tokens":2573073,"output_tokens":267863,"cache_read_tokens":57040896,"cache_write_tokens":0,"total_tokens":59881832,"cost":34.8544786,"actual_cost":3.48544786},{"date":"2026-08-16","requests":10,"input_tokens":16098,"output_tokens":8681,"cache_read_tokens":38400,"cache_write_tokens":0,"total_tokens":63179,"cost":0.146863,"actual_cost":0.0146863},{"date":"2026-08-17","requests":44,"input_tokens":566707,"output_tokens":29166,"cache_read_tokens":3613824,"cache_write_tokens":0,"total_tokens":4209697,"cost":2.04493328,"actual_cost":0.204493328},{"date":"2026-08-18","requests":105,"input_tokens":970355,"output_tokens":54938,"cache_read_tokens":9398272,"cache_write_tokens":0,"total_tokens":10423565,"cost":4.4614422,"actual_cost":0.44614422},{"date":"2026-08-19","requests":433,"input_tokens":3426708,"output_tokens":298621,"cache_read_tokens":52968192,"cache_write_tokens":0,"total_tokens":56693521,"cost":21.0065718,"actual_cost":2.10065718},{"date":"2026-08-20","requests":668,"input_tokens":5717064,"output_tokens":534400,"cache_read_tokens":75678976,"cache_write_tokens":0,"total_tokens":81930440,"cost":32.985634,"actual_cost":3.2985634},{"date":"2026-08-21","requests":55,"input_tokens":1027004,"output_tokens":36839,"cache_read_tokens":3612672,"cache_write_tokens":0,"total_tokens":4676515,"cost":3.2486104,"actual_cost":0.32486104}],"isValid":true,"mode":"unrestricted","model_stats":[{"model":"gpt-5.6-terra","requests":1536,"input_tokens":12845454,"output_tokens":1092091,"cache_creation_tokens":0,"cache_read_tokens":173676544,"total_tokens":187614089,"cost":73.5813088,"actual_cost":7.35813088,"account_cost":73.5813088},{"model":"gpt-5.6-sol","requests":765,"input_tokens":4501482,"output_tokens":441166,"cache_creation_tokens":0,"cache_read_tokens":93135488,"total_tokens":98078136,"cost":82.330134,"actual_cost":8.2330134,"account_cost":82.330134},{"model":"gpt-5.5","requests":135,"input_tokens":1077752,"output_tokens":42178,"cache_creation_tokens":0,"cache_read_tokens":20702592,"total_tokens":21822522,"cost":17.005396,"actual_cost":1.7005396,"account_cost":17.005396},{"model":"gpt-5.6-luna","requests":6,"input_tokens":140115,"output_tokens":631,"cache_creation_tokens":0,"cache_read_tokens":15104,"total_tokens":155850,"cost":0.02908228,"actual_cost":0.002908228,"account_cost":0.02908228},{"model":"gpt-5.4","requests":4,"input_tokens":8107,"output_tokens":6299,"cache_creation_tokens":0,"cache_read_tokens":15360,"total_tokens":29766,"cost":0.1185925,"actual_cost":0.01185925,"account_cost":0.1185925},{"model":"gpt-5.4-mini","requests":3,"input_tokens":6350,"output_tokens":2344,"cache_creation_tokens":0,"cache_read_tokens":11520,"total_tokens":20214,"cost":0.0161745,"actual_cost":0.00161745,"account_cost":0.0161745},{"model":"codex-auto-review","requests":2,"input_tokens":2892,"output_tokens":24,"cache_creation_tokens":0,"cache_read_tokens":0,"total_tokens":2916,"cost":0.0006072,"actual_cost":0.00006072,"account_cost":0.0006072}],"planName":"钱包余额","remaining":7.28784931,"unit":"USD","usage":{"average_duration_ms":17444.60556976414,"rpm":0,"today":{"actual_cost":0,"cache_creation_tokens":0,"cache_read_tokens":0,"cost":0,"input_tokens":0,"output_tokens":0,"requests":0,"total_tokens":0},"total":{"actual_cost":26.533143948,"cache_creation_tokens":0,"cache_read_tokens":370977920,"cost":265.33143948,"input_tokens":36010409,"output_tokens":2017657,"requests":3519,"total_tokens":409005986},"tpm":0}}`)

	report, err := parseUsage(data)
	if err != nil {
		t.Fatal(err)
	}

	if report.Balance == nil || *report.Balance != 7.28784931 {
		t.Fatalf("Balance = %v, want 7.28784931", report.Balance)
	}
	if !report.BalanceStats.HasLimit {
		t.Fatalf("BalanceStats.HasLimit = false, want true")
	}

	// Used should be actual_cost: 26.533143948
	if report.BalanceStats.Used < 26.53 || report.BalanceStats.Used > 26.54 {
		t.Fatalf("BalanceStats.Used = %v, want ~26.533", report.BalanceStats.Used)
	}
	// Limit = 26.533143948 + 7.28784931 = 33.820993258
	if report.BalanceStats.Limit < 33.82 || report.BalanceStats.Limit > 33.83 {
		t.Fatalf("BalanceStats.Limit = %v, want ~33.821", report.BalanceStats.Limit)
	}

	var buf strings.Builder
	printDashboard(&buf, "https://sub2api.example/v1", report, 0, time.Time{})
	out := buf.String()
	t.Logf("Rendered dashboard:\n%s", out)
}

func TestQuotaNumericWalletUsage(t *testing.T) {
	// 真实返回：quota 为数字 1000，balance/remaining = 964.16，used = 35.837
	// 预期余额进度条：已用 35.84 + 剩余 964.16 = 总额 1000.00USD，文案显示"总额"
	data := []byte(`{"balance":964.1629807725001,"daily_usage":[{"date":"2026-08-28","requests":890,"input_tokens":124075710,"output_tokens":334851,"cache_read_tokens":114310717,"cache_write_tokens":0,"total_tokens":124558054,"cost":9.702951367499999,"actual_cost":9.702951367499999},{"date":"2026-08-29","requests":1174,"input_tokens":223342484,"output_tokens":414114,"cache_read_tokens":208657265,"cache_write_tokens":0,"total_tokens":223991205,"cost":26.13406786,"actual_cost":26.13406786}],"isValid":true,"mode":"unrestricted","planName":"CAP Token Usage Tracker","quota":1000,"range":"retention","remaining":964.1629807725001,"unit":"USD","usage":{"average_duration_ms":11152.122426989827,"rpm":0,"today":{"actual_cost":26.134067860000002,"cache_creation_tokens":0,"cache_read_tokens":208657265,"cost":26.134067860000002,"input_tokens":223342484,"output_tokens":414114,"requests":1174,"total_tokens":223991205},"total":{"actual_cost":35.837019227499944,"cache_creation_tokens":0,"cache_read_tokens":322967982,"cost":35.837019227499944,"input_tokens":347418194,"output_tokens":748965,"requests":2064,"total_tokens":348549259},"tpm":0},"used":35.837019227499944}`)

	report, err := parseUsage(data)
	if err != nil {
		t.Fatal(err)
	}

	if !report.BalanceStats.HasLimit {
		t.Fatalf("BalanceStats.HasLimit = false, want true")
	}
	// 已用 = usage.total.actual_cost = 35.837
	if report.BalanceStats.Used < 35.83 || report.BalanceStats.Used > 35.84 {
		t.Fatalf("BalanceStats.Used = %v, want ~35.837", report.BalanceStats.Used)
	}
	// 总额 = 已用 + 剩余 = 35.837019227499944 + 964.1629807725001 = 1000.00
	if report.BalanceStats.Limit < 999.99 || report.BalanceStats.Limit > 1000.01 {
		t.Fatalf("BalanceStats.Limit = %v, want ~1000.00", report.BalanceStats.Limit)
	}

	var buf strings.Builder
	printDashboard(&buf, "https://sub2api.example/v1", report, 0, time.Time{})
	out := buf.String()
	t.Logf("Rendered dashboard:\n%s", out)

	if !strings.Contains(out, "总额：1000.00USD") {
		t.Errorf("output missing '总额：1000.00USD':\n%s", out)
	}
}

func TestFormatExpiry(t *testing.T) {
	got := formatExpiry("2026-09-21T15:07:07.495365+08:00")
	if got != "2026-09-21 15:07:07" {
		t.Fatalf("formatExpiry() = %q", got)
	}
}
