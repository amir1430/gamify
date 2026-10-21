package tigerbeetle_test

import (
	"os"
	"testing"

	"github.com/amir1430/gamify/ledger"
	"github.com/amir1430/gamify/ledger/ledgertest"
	"github.com/amir1430/gamify/ledger/tigerbeetle"

	tb "github.com/tigerbeetle/tigerbeetle-go"
	"github.com/tigerbeetle/tigerbeetle-go/pkg/types"
)

// Runs only against a real TigerBeetle (TEST_TB_ADDRESS).
func TestIntegrationConformance(t *testing.T) {
	addr := os.Getenv("TEST_TB_ADDRESS")
	if addr == "" {
		t.Skip("TEST_TB_ADDRESS not set")
	}
	client, err := tb.NewClient(types.ToUint128(0), []string{addr})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	ledgertest.Run(t, func(*testing.T) ledger.Driver { return tigerbeetle.New(client) })
}
