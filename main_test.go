package main

import (
	"os"
	"path/filepath"
	"testing"
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

func TestFormatExpiry(t *testing.T) {
	got := formatExpiry("2026-09-21T15:07:07.495365+08:00")
	if got != "2026-09-21 15:07:07" {
		t.Fatalf("formatExpiry() = %q", got)
	}
}
