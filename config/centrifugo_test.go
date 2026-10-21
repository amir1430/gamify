package config

import (
	"bytes"
	"flag"
	"os"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

func fullConfig(t *testing.T) *Config {
	t.Helper()
	data, err := os.ReadFile("testdata/full.yaml")
	if err != nil {
		t.Fatal(err)
	}
	c, err := Parse(data, envOf(map[string]string{
		"DATABASE_URL": "postgres://u:p@h/db", "CENTRIFUGO_API_KEY": "APIKEYVALUE", "CENTRIFUGO_TOKEN_SECRET": "TOKENSECRETVALUE"}))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCentrifugoJSON(t *testing.T) {
	got := fullConfig(t).CentrifugoJSON()
	const golden = "testdata/centrifugo.golden.json"
	if *update {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestCentrifugoJSONStable(t *testing.T) {
	c := fullConfig(t)
	if !bytes.Equal(c.CentrifugoJSON(), c.CentrifugoJSON()) {
		t.Fatal("two renders differ")
	}
}

func TestCentrifugoJSONNoSecrets(t *testing.T) {
	out := string(fullConfig(t).CentrifugoJSON())
	for _, s := range []string{"APIKEYVALUE", "TOKENSECRETVALUE", `"key"`, `"hmac_secret_key"`} {
		if strings.Contains(out, s) {
			t.Errorf("config.json contains %s", s)
		}
	}
}
