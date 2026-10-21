package config

import (
	"log/slog"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

var secrets = map[string]string{
	"DATABASE_URL":            "postgres://u:p@h/db",
	"CENTRIFUGO_API_KEY":      "k",
	"CENTRIFUGO_TOKEN_SECRET": "s",
}

func envOf(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

// required is the smallest valid file.
const required = `
postgres: {dsn: "${DATABASE_URL}"}
centrifugo:
  api_addr: centrifugo:10000
  api_key: ${CENTRIFUGO_API_KEY}
  token_secret: ${CENTRIFUGO_TOKEN_SECRET}
  proxy_endpoint: grpc://api:10001
http: {allowed_origins: [https://mini.example]}
`

func TestFull(t *testing.T) {
	data, err := os.ReadFile("testdata/full.yaml")
	if err != nil {
		t.Fatal(err)
	}
	c, err := Parse(data, envOf(secrets))
	if err != nil {
		t.Fatal(err)
	}
	want := &Config{
		Postgres: Postgres{DSN: "postgres://u:p@h/db", MaxConns: 50},
		Ledger: Ledger{Driver: "tigerbeetle", TigerBeetle: &TigerBeetle{ClusterID: 7,
			Addresses: []string{"tb-0:3000", "tb-1:3000", "tb-2:3000"}}},
		Centrifugo: Centrifugo{APIAddr: "centrifugo:10000", APIKey: "k", TokenSecret: "s", TokenTTL: time.Hour,
			ProxyListen: ":10001", ProxyEndpoint: "grpc://api:10001", ProxyTimeout: 5 * time.Second},
		HTTP:    HTTP{Addr: ":8081", AllowedOrigins: []string{"https://mini.example"}},
		Metrics: Metrics{Addr: "off"},
		Log:     Log{Level: slog.LevelInfo},
	}
	if !reflect.DeepEqual(c, want) {
		t.Fatalf("got  %+v\nwant %+v", c, want)
	}
}

func TestDefaults(t *testing.T) {
	c, err := Parse([]byte(required), envOf(secrets))
	if err != nil {
		t.Fatal(err)
	}
	if c.Postgres.MaxConns != 30 || c.Ledger.Driver != "postgres" || c.Centrifugo.TokenTTL != time.Hour ||
		c.Centrifugo.ProxyListen != ":10001" || c.Centrifugo.ProxyTimeout != 5*time.Second ||
		c.HTTP.Addr != ":8080" || c.Metrics.Addr != ":9090" || c.Log.Level != slog.LevelInfo {
		t.Fatalf("defaults: %+v", c)
	}
}

func TestSecretsVerbatim(t *testing.T) {
	env := map[string]string{"DATABASE_URL": "x", "CENTRIFUGO_API_KEY": `a #b: *c "d`, "CENTRIFUGO_TOKEN_SECRET": "s"}
	c, err := Parse([]byte(required), envOf(env))
	if err != nil {
		t.Fatal(err)
	}
	if c.Centrifugo.APIKey != env["CENTRIFUGO_API_KEY"] {
		t.Fatalf("api key = %q", c.Centrifugo.APIKey)
	}
	c, err = Parse([]byte(strings.Replace(required, "${CENTRIFUGO_TOKEN_SECRET}", `"x$$y"`, 1)), envOf(env))
	if err != nil {
		t.Fatal(err)
	}
	if c.Centrifugo.TokenSecret != "x$y" {
		t.Fatalf("token secret = %q", c.Centrifugo.TokenSecret)
	}
}

func TestValidate(t *testing.T) {
	without := func(k string) map[string]string {
		m := map[string]string{}
		for kk, v := range secrets {
			if kk != k {
				m[kk] = v
			}
		}
		return m
	}
	for _, tc := range []struct {
		name, yaml string
		env        map[string]string
		want       []string
	}{
		{"unknown key", required + "\nmetrics: {adr: x}", secrets, []string{"adr"}},
		{"unset var", required, without("CENTRIFUGO_API_KEY"), []string{"centrifugo.api_key: ${CENTRIFUGO_API_KEY} is not set"}},
		{"empty var", required, map[string]string{"DATABASE_URL": "x", "CENTRIFUGO_API_KEY": "", "CENTRIFUGO_TOKEN_SECRET": "s"},
			[]string{"centrifugo.api_key: ${CENTRIFUGO_API_KEY} is not set"}},
		{"bad driver", required + "\nledger: {driver: tigerbeetl}", secrets, []string{"ledger.driver"}},
		{"tb section on postgres", required + "\nledger: {tigerbeetle: {addresses: [a:1]}}", secrets, []string{"ledger.tigerbeetle"}},
		{"tb driver without section", required + "\nledger: {driver: tigerbeetle}", secrets, []string{"ledger.tigerbeetle"}},
		{"tb without addresses", required + "\nledger: {driver: tigerbeetle, tigerbeetle: {cluster_id: 1}}", secrets, []string{"ledger.tigerbeetle.addresses"}},
		{"star origin", strings.Replace(required, "https://mini.example", `"*"`, 1), secrets, []string{"http.allowed_origins"}},
		{"no origins", strings.Replace(required, "http: {allowed_origins: [https://mini.example]}", "", 1), secrets, []string{"http.allowed_origins"}},
		{"api_addr without port", strings.Replace(required, "centrifugo:10000", "centrifugo", 1), secrets, []string{"centrifugo.api_addr"}},
		{"api_addr bad port", strings.Replace(required, "centrifugo:10000", "centrifugo:grpc", 1), secrets, []string{"centrifugo.api_addr"}},
		{"bad level", required + "\nlog: {level: loud}", secrets, []string{"log.level"}},
		{"negative ttl", strings.Replace(required, "proxy_endpoint", "token_ttl: -1h\n  proxy_endpoint", 1), secrets, []string{"centrifugo.token_ttl"}},
		{"negative proxy timeout", strings.Replace(required, "proxy_endpoint", "proxy_timeout: -2s\n  proxy_endpoint", 1), secrets, []string{"centrifugo.proxy_timeout"}},
		{"negative max conns", strings.Replace(required, `postgres: {dsn: "${DATABASE_URL}"}`, `postgres: {dsn: "${DATABASE_URL}", max_conns: -1}`, 1), secrets, []string{"postgres.max_conns"}},
		{"origin with path", strings.Replace(required, "https://mini.example", "https://mini.example/", 1), secrets, []string{"http.allowed_origins"}},
		{"origin without scheme", strings.Replace(required, "https://mini.example", "mini.example", 1), secrets, []string{"http.allowed_origins"}},
		{"proxy endpoint without scheme", strings.Replace(required, "grpc://api:10001", "api:10001", 1), secrets, []string{"centrifugo.proxy_endpoint"}},
		{"empty tb address", required + "\nledger: {driver: tigerbeetle, tigerbeetle: {addresses: [\"\"]}}", secrets, []string{"ledger.tigerbeetle.addresses"}},
		{"second document", required + "---\nbogus: 1\n", secrets, []string{"one document"}},
		{"empty file", "", secrets, []string{"postgres.dsn", "centrifugo.api_addr", "centrifugo.api_key",
			"centrifugo.token_secret", "centrifugo.proxy_endpoint", "http.allowed_origins"}},
		{"only comments", "# nothing\n", secrets, []string{"postgres.dsn", "http.allowed_origins"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.yaml), envOf(tc.env))
			if err == nil {
				t.Fatal("accepted")
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error lacks %q:\n%v", w, err)
				}
			}
		})
	}
}

func TestNoSecretInErrors(t *testing.T) {
	env := map[string]string{"DATABASE_URL": "SUPERSECRET", "CENTRIFUGO_API_KEY": "SUPERSECRET", "CENTRIFUGO_TOKEN_SECRET": "SUPERSECRET"}
	for _, y := range []string{
		required + "\nledger: {driver: nope}\n",
		strings.Replace(required, "proxy_endpoint", "token_ttl: soon\n  proxy_endpoint", 1),
		required + "\ncentrifugo: [\n",
		"postgres: {dsn: ${DATABASE_URL}, max_conns: ${DATABASE_URL}}\n",
	} {
		_, err := Parse([]byte(y), envOf(env))
		if err == nil {
			t.Fatalf("accepted:\n%s", y)
		}
		if strings.Contains(err.Error(), "SUPERSECRET") {
			t.Fatalf("error leaks the secret: %v", err)
		}
	}
}
