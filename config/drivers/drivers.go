// Package drivers builds the drivers a config.Config describes: the Postgres pool, the ledger
// driver and the Centrifugo transport. Separate from config because the TigerBeetle client is cgo.
package drivers

import (
	"context"
	"strings"
	"sync"

	"github.com/amir1430/gamify/app"
	"github.com/amir1430/gamify/config"
	"github.com/amir1430/gamify/db"
	"github.com/amir1430/gamify/ledger/postgres"
	"github.com/amir1430/gamify/ledger/tigerbeetle"
	"github.com/amir1430/gamify/realtime/centrifugo"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Build opens everything c describes (the Postgres ledger's migrations run here). closeAll releases
// it in reverse order; on error, whatever was opened is already closed.
func Build(ctx context.Context, c *config.Config) (d app.Drivers, closeAll func(), err error) {
	var closers []func()
	closeAll = sync.OnceFunc(func() {
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i]()
		}
	})
	defer func() {
		if err != nil {
			closeAll()
		}
	}()

	pcfg, err := pgxpool.ParseConfig(c.Postgres.DSN)
	if err != nil {
		return d, nil, err
	}
	if !strings.Contains(c.Postgres.DSN, "pool_max_conns") {
		pcfg.MaxConns = c.Postgres.MaxConns
	}
	if d.Pool, err = pgxpool.NewWithConfig(ctx, pcfg); err != nil {
		return d, nil, err
	}
	closers = append(closers, d.Pool.Close)

	switch c.Ledger.Driver {
	case "postgres":
		if err = db.Migrate(ctx, d.Pool, postgres.Migrations, "goose_ledger"); err != nil {
			return d, nil, err
		}
		d.Ledger = postgres.New(d.Pool)
	case "tigerbeetle":
		tb, err := tigerbeetle.Dial(ctx, c.Ledger.TigerBeetle.ClusterID, strings.Join(c.Ledger.TigerBeetle.Addresses, ","))
		if err != nil {
			return d, nil, err
		}
		closers = append(closers, tb.Close)
		d.Ledger = tb
	}

	rt, err := centrifugo.New(centrifugo.Config{APIAddr: c.Centrifugo.APIAddr, APIKey: c.Centrifugo.APIKey,
		ProxyAddr: c.Centrifugo.ProxyListen, TokenSecret: []byte(c.Centrifugo.TokenSecret), TokenTTL: c.Centrifugo.TokenTTL})
	if err != nil {
		return d, nil, err
	}
	closers = append(closers, func() { rt.Close() })
	d.Realtime = rt
	return d, closeAll, nil
}
