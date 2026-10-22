package main

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/amir1430/gamify/admin"
	"github.com/amir1430/gamify/ledger"
)

func TestParseCurrencies(t *testing.T) {
	got, err := parseCurrencies("usd:1:1:0:2, tickets:2:10:1000,coins:3:5")
	if err != nil {
		t.Fatal(err)
	}
	want := []ledger.CurrencyConfig{{Code: "usd", Ledger: 1, MinAmount: 1, Decimals: 2},
		{Code: "tickets", Ledger: 2, MinAmount: 10, Welcome: 1000}, {Code: "coins", Ledger: 3, MinAmount: 5}}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%d: %+v, want %+v", i, got[i], want[i])
		}
	}
	gems, err := parseCurrencies("gems:3:1:0:0:t,usd:1:1")
	if err != nil || !gems[0].Transferable || gems[1].Transferable {
		t.Fatalf("transferable: %+v, %v", gems, err)
	}
	for _, bad := range []string{"usd:1", "usd:1:1:0:2:9", "gems:3:1:0:0:x", "gems:3:1:0:0:t:1", "usd:1:1:0:19", "usd:1:1:0:256", "usd:1:1:0:-1", "usd:x:1"} {
		if _, err := parseCurrencies(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestAdminConfigFromEnv(t *testing.T) {
	key := strings.Repeat("ab", 32)
	get := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

	if _, on, err := adminConfig(get(nil)); on || err != nil {
		t.Fatalf("unset: on=%v err=%v", on, err)
	}
	cfg, on, err := adminConfig(get(map[string]string{
		"ADMIN_ADDR": ":8081", "ADMIN_ORIGIN": "http://localhost:8081", "ADMIN_TOTP_KEY": key,
		"ADMIN_ALLOW_IPS":     "10.0.0.0/8, 192.168.1.0/24",
		"ADMIN_APPROVE_ABOVE": "usd:10000,eur:5000", "TG_BOT_TOKEN": "123:x",
	}))
	if err != nil || !on {
		t.Fatalf("on=%v err=%v", on, err)
	}
	if cfg.Addr != ":8081" || cfg.Origin != "http://localhost:8081" || len(cfg.TOTPKey) != 32 || cfg.TelegramBotToken != "123:x" {
		t.Fatalf("%+v", cfg)
	}
	if len(cfg.AllowIPs) != 2 || cfg.AllowIPs[0] != netip.MustParsePrefix("10.0.0.0/8") || cfg.AllowIPs[1] != netip.MustParsePrefix("192.168.1.0/24") {
		t.Fatalf("allow: %v", cfg.AllowIPs)
	}
	if len(cfg.ApproveAbove) != 2 || cfg.ApproveAbove["usd"] != 10000 || cfg.ApproveAbove["eur"] != 5000 {
		t.Fatalf("approve: %v", cfg.ApproveAbove)
	}

	base := map[string]string{"ADMIN_ADDR": ":8081", "ADMIN_ORIGIN": "http://x", "ADMIN_TOTP_KEY": key}
	with := func(k, v string) map[string]string {
		m := map[string]string{}
		for a, b := range base {
			m[a] = b
		}
		m[k] = v
		return m
	}
	for name, m := range map[string]map[string]string{
		"no key":      {"ADMIN_ADDR": ":8081", "ADMIN_ORIGIN": "http://x"},
		"short key":   with("ADMIN_TOTP_KEY", "abcd"),
		"non-hex key": with("ADMIN_TOTP_KEY", strings.Repeat("zz", 32)),
		"bad cidr":    with("ADMIN_ALLOW_IPS", "10.0.0.0"),
		"bad approve": with("ADMIN_APPROVE_ABOVE", "usd"),
		"neg approve": with("ADMIN_APPROVE_ABOVE", "usd:-1"),
		"nan approve": with("ADMIN_APPROVE_ABOVE", "usd:x"),
		"empty ccy":   with("ADMIN_APPROVE_ABOVE", ":5"),
	} {
		if _, _, err := adminConfig(get(m)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestParseRole(t *testing.T) {
	for _, r := range []string{"owner", "operator", "support", "viewer"} {
		if got, err := parseRole(r); err != nil || got != admin.Role(r) {
			t.Errorf("%s: %v %v", r, got, err)
		}
	}
	if _, err := parseRole("root"); err == nil {
		t.Error("root accepted")
	}
}
