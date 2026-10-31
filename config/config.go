// Package config reads a brand's infra settings from one YAML file (gamify.yaml): Postgres, the
// ledger driver, Centrifugo, listen addresses, log level. Secrets are never written in it: a string
// value may reference the environment as ${VAR}, expanded after parsing, so a secret can't change
// the YAML around it and no error ever prints one. The same file generates Centrifugo's config
// (CentrifugoJSON). Building the drivers is config/drivers: this package needs no cgo.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/amir1430/gamify/app"

	"go.yaml.in/yaml/v3"
)

type Config struct {
	Postgres   Postgres   `yaml:"postgres"`
	Ledger     Ledger     `yaml:"ledger"`
	Centrifugo Centrifugo `yaml:"centrifugo"`
	HTTP       HTTP       `yaml:"http"`
	Metrics    Metrics    `yaml:"metrics"`
	Log        Log        `yaml:"log"`
}

type Postgres struct {
	DSN      string `yaml:"dsn"`
	MaxConns int32  `yaml:"max_conns"` // default 30; a pool_max_conns in the DSN wins
}

type Ledger struct {
	Driver      string       `yaml:"driver"`      // postgres (default) | tigerbeetle
	TigerBeetle *TigerBeetle `yaml:"tigerbeetle"` // only with the tigerbeetle driver
}

type TigerBeetle struct {
	ClusterID uint64   `yaml:"cluster_id"`
	Addresses []string `yaml:"addresses"`
}

type Centrifugo struct {
	APIAddr       string        `yaml:"api_addr"` // the app dials it; its port is Centrifugo's grpc_api.port
	APIKey        string        `yaml:"api_key"`
	TokenSecret   string        `yaml:"token_secret"`
	TokenTTL      time.Duration `yaml:"token_ttl"`      // default 1h
	ProxyListen   string        `yaml:"proxy_listen"`   // where the app serves the RPC proxy, default :10001
	ProxyEndpoint string        `yaml:"proxy_endpoint"` // how Centrifugo reaches it, e.g. grpc://api:10001
	ProxyTimeout  time.Duration `yaml:"proxy_timeout"`  // default 5s
	// Namespaces are the brand's own channel namespaces (its rooms, tables, prices), added to the
	// platform's (user, online, feed) in Centrifugo's config.
	Namespaces []Namespace `yaml:"namespaces"`
	// RPCNamespaces, when set, routes RPCs by namespace (a method's part before "."): it lists
	// every namespace the app serves, the platform's included (seed, wallet, …). A namespace in
	// none of Proxies goes to proxy_endpoint; Centrifugo refuses a method whose namespace isn't
	// listed. Unset: every RPC goes to proxy_endpoint.
	RPCNamespaces []string `yaml:"rpc_namespaces"`
	// Proxies send some RPC namespaces to another endpoint, e.g. single-instance game rooms
	// deployed apart from the app. Requires RPCNamespaces. Timeout: proxy_timeout.
	Proxies []Proxy `yaml:"proxies"`
}

// Proxy is a named RPC proxy: Centrifugo sends methods of its namespaces to Endpoint.
type Proxy struct {
	Name       string   `yaml:"name"`
	Endpoint   string   `yaml:"endpoint"` // grpc://host:port
	Namespaces []string `yaml:"namespaces"`
}

// Namespace is a channel namespace the brand's games publish to: channels "<name>:…".
type Namespace struct {
	Name      string `yaml:"name"`      // letters, digits, - and _
	Presence  bool   `yaml:"presence"`  // Centrifugo tracks who is subscribed (Online counts)
	Subscribe bool   `yaml:"subscribe"` // clients may subscribe themselves (public channels)
}

// platformNamespaces are the namespaces gamify's own code publishes to; a brand can't redefine them.
var platformNamespaces = []string{"user", "online", "feed"}

type HTTP struct {
	Addr           string   `yaml:"addr"`            // default :8080
	AllowedOrigins []string `yaml:"allowed_origins"` // app CORS and Centrifugo's client origins
}

type Metrics struct {
	Addr string `yaml:"addr"` // default :9090, "off" disables
}

type Log struct {
	Level slog.Level `yaml:"level"` // default info
}

// UnmarshalYAML puts the path on a bad level (slog's own error has none).
func (l *Log) UnmarshalYAML(n *yaml.Node) error {
	var raw struct {
		Level string `yaml:"level"`
	}
	if err := n.Decode(&raw); err != nil {
		return err
	}
	if len(n.Content) > 2 || (len(n.Content) == 2 && n.Content[0].Value != "level") {
		return fmt.Errorf("log: only level is allowed")
	}
	if raw.Level == "" {
		return nil
	}
	if err := l.Level.UnmarshalText([]byte(raw.Level)); err != nil {
		return fmt.Errorf("log.level: %q: want debug, info, warn or error", raw.Level)
	}
	return nil
}

// Load reads path and parses it with the process environment.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data, os.LookupEnv)
}

// Parse decodes data strictly (unknown keys are errors), applies defaults, expands ${VAR} in string
// values with env, and validates. Every problem comes back at once, each prefixed by its path.
func Parse(data []byte, env func(string) (string, bool)) (*Config, error) {
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	// Decoding happens before expansion, so a decode error can only quote ${VAR}, never a secret.
	if err := dec.Decode(&c); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("config: %w", err)
	}
	if err := dec.Decode(&yaml.Node{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("config: only one document is allowed (remove the extra ---)")
	}
	c.defaults()
	var errs []error
	unset := map[string]bool{}
	expandAll(reflect.ValueOf(&c).Elem(), "", env, func(path string, err error) {
		unset[path] = true
		errs = append(errs, fmt.Errorf("%s: %w", path, err))
	})
	errs = append(errs, c.validate(unset)...)
	if len(errs) > 0 {
		return nil, fmt.Errorf("config: %w", errors.Join(errs...))
	}
	return &c, nil
}

// App is the infra part of app.Config; the brand adds currencies, bot token and sealer.
func (c *Config) App() app.Config {
	return app.Config{HTTPAddr: c.HTTP.Addr, MetricsAddr: c.Metrics.Addr, AllowedOrigins: c.HTTP.AllowedOrigins}
}

func (c *Config) defaults() {
	def := func(s *string, v string) {
		if *s == "" {
			*s = v
		}
	}
	if c.Postgres.MaxConns == 0 {
		c.Postgres.MaxConns = 30
	}
	def(&c.Ledger.Driver, "postgres")
	if c.Centrifugo.TokenTTL == 0 {
		c.Centrifugo.TokenTTL = time.Hour
	}
	def(&c.Centrifugo.ProxyListen, ":10001")
	if c.Centrifugo.ProxyTimeout == 0 {
		c.Centrifugo.ProxyTimeout = 5 * time.Second
	}
	def(&c.HTTP.Addr, ":8080")
	def(&c.Metrics.Addr, ":9090")
}

func (c *Config) validate(unset map[string]bool) []error {
	var errs []error
	bad := func(path, format string, args ...any) {
		errs = append(errs, fmt.Errorf("%s: "+format, append([]any{path}, args...)...))
	}
	required := func(path, v string) {
		if v == "" && !unset[path] {
			bad(path, "required")
		}
	}
	required("postgres.dsn", c.Postgres.DSN)
	if c.Postgres.MaxConns < 1 {
		bad("postgres.max_conns", "%d: must be at least 1", c.Postgres.MaxConns)
	}
	switch c.Ledger.Driver {
	case "postgres":
		if c.Ledger.TigerBeetle != nil {
			bad("ledger.tigerbeetle", "set, but the driver is postgres")
		}
	case "tigerbeetle":
		if c.Ledger.TigerBeetle == nil {
			bad("ledger.tigerbeetle", "required with the tigerbeetle driver")
		} else if len(c.Ledger.TigerBeetle.Addresses) == 0 || slices.Contains(c.Ledger.TigerBeetle.Addresses, "") {
			bad("ledger.tigerbeetle.addresses", "required, no empty entries")
		}
	default:
		bad("ledger.driver", "%q: want postgres or tigerbeetle", c.Ledger.Driver)
	}
	if c.Centrifugo.APIAddr == "" {
		required("centrifugo.api_addr", "")
	} else if _, err := grpcPort(c.Centrifugo.APIAddr); err != nil {
		bad("centrifugo.api_addr", "%v", err)
	}
	required("centrifugo.api_key", c.Centrifugo.APIKey)
	required("centrifugo.token_secret", c.Centrifugo.TokenSecret)
	if c.Centrifugo.TokenTTL <= 0 {
		bad("centrifugo.token_ttl", "%s: must be positive", c.Centrifugo.TokenTTL)
	}
	if c.Centrifugo.ProxyTimeout <= 0 {
		bad("centrifugo.proxy_timeout", "%s: must be positive", c.Centrifugo.ProxyTimeout)
	}
	if c.Centrifugo.ProxyEndpoint == "" {
		required("centrifugo.proxy_endpoint", "")
	} else if !validEndpoint(c.Centrifugo.ProxyEndpoint) {
		bad("centrifugo.proxy_endpoint", "%q: want grpc://host:port", c.Centrifugo.ProxyEndpoint)
	}
	rpcSeen := map[string]bool{}
	for i, ns := range c.Centrifugo.RPCNamespaces {
		path := fmt.Sprintf("centrifugo.rpc_namespaces[%d]", i)
		switch {
		case !validNamespace(ns) || strings.Contains(ns, "."):
			bad(path, "%q: want letters, digits, - or _", ns)
		case rpcSeen[ns]:
			bad(path, "%q listed twice", ns)
		}
		rpcSeen[ns] = true
	}
	if len(c.Centrifugo.Proxies) > 0 && len(c.Centrifugo.RPCNamespaces) == 0 {
		bad("centrifugo.proxies", "set, but centrifugo.rpc_namespaces isn't: list every RPC namespace")
	}
	proxySeen, routed := map[string]bool{}, map[string]string{}
	for i, p := range c.Centrifugo.Proxies {
		path := fmt.Sprintf("centrifugo.proxies[%d]", i)
		switch {
		case !validNamespace(p.Name):
			bad(path+".name", "%q: want letters, digits, - or _", p.Name)
		case proxySeen[p.Name]:
			bad(path+".name", "%q listed twice", p.Name)
		}
		proxySeen[p.Name] = true
		if !validEndpoint(p.Endpoint) {
			bad(path+".endpoint", "%q: want grpc://host:port", p.Endpoint)
		}
		if len(p.Namespaces) == 0 {
			bad(path+".namespaces", "required: at least one")
		}
		for _, ns := range p.Namespaces {
			switch {
			case !rpcSeen[ns]:
				bad(path+".namespaces", "%q isn't in centrifugo.rpc_namespaces", ns)
			case routed[ns] != "":
				bad(path+".namespaces", "%q is already proxy %q's", ns, routed[ns])
			default:
				routed[ns] = p.Name
			}
		}
	}
	seen := map[string]bool{}
	for i, ns := range c.Centrifugo.Namespaces {
		path := fmt.Sprintf("centrifugo.namespaces[%d].name", i)
		switch {
		case !validNamespace(ns.Name):
			bad(path, "%q: want letters, digits, - or _", ns.Name)
		case slices.Contains(platformNamespaces, ns.Name):
			bad(path, "%q is the platform's", ns.Name)
		case seen[ns.Name]:
			bad(path, "%q listed twice", ns.Name)
		}
		seen[ns.Name] = true
	}
	if len(c.HTTP.AllowedOrigins) == 0 {
		bad("http.allowed_origins", "required: at least one origin")
	}
	for _, o := range c.HTTP.AllowedOrigins {
		if o == "*" {
			bad("http.allowed_origins", "\"*\" is refused: list each origin")
		} else if !isOrigin(o) {
			bad("http.allowed_origins", "%q: want scheme://host[:port], no path (what a browser sends as Origin)", o)
		}
	}
	return errs
}

func validEndpoint(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Host != "" && slices.Contains([]string{"grpc", "http", "https"}, u.Scheme)
}

// validNamespace reports whether name is a Centrifugo namespace name (no ':' or other separators).
func validNamespace(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// isOrigin reports whether o is a browser Origin: scheme and host, nothing after.
func isOrigin(o string) bool {
	u, err := url.Parse(o)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" &&
		u.Path == "" && u.RawQuery == "" && u.Fragment == "" && u.User == nil
}

// grpcPort is the port of host:port, as Centrifugo's grpc_api.port.
func grpcPort(addr string) (int, error) {
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		return 0, fmt.Errorf("%q: want host:port", addr)
	}
	port, err := strconv.Atoi(p)
	if err != nil || port <= 0 || port > 65535 {
		return 0, fmt.Errorf("%q: bad port", addr)
	}
	return port, nil
}

// expandAll expands ${VAR} in every string and []string field under v, reporting each failure with
// the field's YAML path.
func expandAll(v reflect.Value, path string, env func(string) (string, bool), fail func(string, error)) {
	switch v.Kind() {
	case reflect.Pointer:
		if !v.IsNil() {
			expandAll(v.Elem(), path, env, fail)
		}
	case reflect.Struct:
		for i := range v.NumField() {
			name, _, _ := strings.Cut(v.Type().Field(i).Tag.Get("yaml"), ",")
			if path != "" {
				name = path + "." + name
			}
			expandAll(v.Field(i), name, env, fail)
		}
	case reflect.String:
		s, err := expand(v.String(), env)
		if err != nil {
			fail(path, err)
		}
		v.SetString(s)
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.String {
			for i := range v.Len() {
				expandAll(v.Index(i), path, env, fail)
			}
		}
	}
}

// expand replaces ${NAME} with its value and $$ with $; any other $ stays. An unset or empty
// variable is an error naming it (never its value).
func expand(s string, env func(string) (string, bool)) (string, error) {
	if !strings.Contains(s, "$") {
		return s, nil
	}
	var b strings.Builder
	for {
		i := strings.IndexByte(s, '$')
		if i < 0 || i == len(s)-1 {
			b.WriteString(s)
			return b.String(), nil
		}
		b.WriteString(s[:i])
		switch s[i+1] {
		case '$':
			b.WriteByte('$')
			s = s[i+2:]
		case '{':
			end := strings.IndexByte(s[i:], '}')
			if end < 0 {
				return "", fmt.Errorf("unclosed ${")
			}
			name := s[i+2 : i+end]
			v, ok := env(name)
			if !ok || v == "" {
				return "", fmt.Errorf("${%s} is not set", name)
			}
			b.WriteString(v)
			s = s[i+end+1:]
		default:
			b.WriteByte('$')
			s = s[i+1:]
		}
	}
}
