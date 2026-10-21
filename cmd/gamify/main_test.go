package main

import (
	"testing"

	"github.com/amir1430/gamify/ledger"
)

func TestParseCurrencies(t *testing.T) {
	got, err := parseCurrencies("usd:1:1:0:2, tickets:2:10:1000,coins:3:5")
	if err != nil {
		t.Fatal(err)
	}
	want := []ledger.CurrencyConfig{{Code: "usd", Ledger: 1, MinBet: 1, Decimals: 2},
		{Code: "tickets", Ledger: 2, MinBet: 10, Welcome: 1000}, {Code: "coins", Ledger: 3, MinBet: 5}}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%d: %+v, want %+v", i, got[i], want[i])
		}
	}
	for _, bad := range []string{"usd:1", "usd:1:1:0:2:9", "usd:1:1:0:19", "usd:1:1:0:256", "usd:1:1:0:-1", "usd:x:1"} {
		if _, err := parseCurrencies(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
