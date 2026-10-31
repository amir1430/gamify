package config

import "encoding/json"

// Centrifugo's config.json, in Centrifugo v6's layout. Structs, not maps: the field order is fixed,
// so an unchanged YAML regenerates a byte-identical file.
type centrifugoConfig struct {
	Health struct {
		Enabled bool `json:"enabled"`
	} `json:"health"`
	Client struct {
		AllowedOrigins []string `json:"allowed_origins"`
	} `json:"client"`
	GRPCAPI struct {
		Enabled bool `json:"enabled"`
		Port    int  `json:"port"`
	} `json:"grpc_api"`
	Channel struct {
		Namespaces []namespace `json:"namespaces"`
	} `json:"channel"`
	RPC struct {
		Proxy struct {
			Endpoint string `json:"endpoint"`
			Timeout  string `json:"timeout"`
		} `json:"proxy"`
		WithoutNamespace struct {
			ProxyEnabled bool `json:"proxy_enabled"`
		} `json:"without_namespace"`
		NamespaceBoundary string         `json:"namespace_boundary,omitempty"`
		Namespaces        []rpcNamespace `json:"namespaces,omitempty"`
	} `json:"rpc"`
	Proxies []proxy `json:"proxies,omitempty"`
}

type rpcNamespace struct {
	Name         string `json:"name"`
	ProxyEnabled bool   `json:"proxy_enabled"`
	ProxyName    string `json:"proxy_name,omitempty"`
}

type proxy struct {
	Name     string `json:"name"`
	Endpoint string `json:"endpoint"`
	Timeout  string `json:"timeout"`
}

type namespace struct {
	Name                    string `json:"name"`
	Presence                bool   `json:"presence,omitempty"`
	AllowSubscribeForClient bool   `json:"allow_subscribe_for_client,omitempty"`
}

// CentrifugoJSON is Centrifugo's config.json for this file. It never holds the API key or the token
// secret: Centrifugo reads them from its own env (CENTRIFUGO_GRPC_API_KEY,
// CENTRIFUGO_CLIENT_TOKEN_HMAC_SECRET_KEY), set to the same values as the app's.
func (c *Config) CentrifugoJSON() []byte {
	var out centrifugoConfig
	out.Health.Enabled = true
	out.Client.AllowedOrigins = c.HTTP.AllowedOrigins
	out.GRPCAPI.Enabled = true
	out.GRPCAPI.Port, _ = grpcPort(c.Centrifugo.APIAddr) // validated by Parse
	// The namespaces the platform's code publishes to.
	out.Channel.Namespaces = []namespace{{Name: "user"}, {Name: "online", Presence: true}, {Name: "feed", AllowSubscribeForClient: true}}
	// Then the brand's own.
	for _, ns := range c.Centrifugo.Namespaces {
		out.Channel.Namespaces = append(out.Channel.Namespaces, namespace{Name: ns.Name, Presence: ns.Presence, AllowSubscribeForClient: ns.Subscribe})
	}
	out.RPC.Proxy.Endpoint = c.Centrifugo.ProxyEndpoint
	out.RPC.Proxy.Timeout = c.Centrifugo.ProxyTimeout.String()
	out.RPC.WithoutNamespace.ProxyEnabled = true
	// Routing by namespace: "dice.bet" is namespace "dice", sent to its proxy (default or named).
	if len(c.Centrifugo.RPCNamespaces) > 0 {
		out.RPC.NamespaceBoundary = "."
		owner := map[string]string{}
		for _, p := range c.Centrifugo.Proxies {
			out.Proxies = append(out.Proxies, proxy{Name: p.Name, Endpoint: p.Endpoint, Timeout: c.Centrifugo.ProxyTimeout.String()})
			for _, ns := range p.Namespaces {
				owner[ns] = p.Name
			}
		}
		for _, ns := range c.Centrifugo.RPCNamespaces {
			out.RPC.Namespaces = append(out.RPC.Namespaces, rpcNamespace{Name: ns, ProxyEnabled: true, ProxyName: owner[ns]})
		}
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	return append(b, '\n')
}
