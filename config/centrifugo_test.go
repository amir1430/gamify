package config

import (
	"bytes"
	"encoding/json"
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

// rpc_namespaces + proxies: methods routed by namespace, the named proxy's to its endpoint.
func TestCentrifugoJSONRouting(t *testing.T) {
	data, err := os.ReadFile("testdata/full.yaml")
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte("  namespaces:"), []byte(`  rpc_namespaces: [dice, crash, seed]
  proxies:
    - {name: rooms, endpoint: "grpc://rooms:10001", namespaces: [crash]}
  namespaces:`), 1)
	c, err := Parse(data, envOf(map[string]string{
		"DATABASE_URL": "postgres://u:p@h/db", "CENTRIFUGO_API_KEY": "k", "CENTRIFUGO_TOKEN_SECRET": "s"}))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		RPC struct {
			NamespaceBoundary string `json:"namespace_boundary"`
			Namespaces        []struct {
				Name         string `json:"name"`
				ProxyEnabled bool   `json:"proxy_enabled"`
				ProxyName    string `json:"proxy_name"`
			} `json:"namespaces"`
		} `json:"rpc"`
		Proxies []struct{ Name, Endpoint, Timeout string } `json:"proxies"`
	}
	if err := json.Unmarshal(c.CentrifugoJSON(), &got); err != nil {
		t.Fatal(err)
	}
	ns := got.RPC.Namespaces
	if got.RPC.NamespaceBoundary != "." || len(ns) != 3 || ns[0].Name != "dice" || ns[0].ProxyName != "" || !ns[0].ProxyEnabled ||
		ns[1].Name != "crash" || ns[1].ProxyName != "rooms" || ns[2].Name != "seed" || ns[2].ProxyName != "" {
		t.Fatalf("rpc: %+v", got.RPC)
	}
	if len(got.Proxies) != 1 || got.Proxies[0].Name != "rooms" || got.Proxies[0].Endpoint != "grpc://rooms:10001" || got.Proxies[0].Timeout != "5s" {
		t.Fatalf("proxies: %+v", got.Proxies)
	}
}
