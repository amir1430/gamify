// Package tigerbeetle is the TigerBeetle ledger driver. The Go client version must equal the
// server version (0.16.46).
package tigerbeetle

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"strings"

	"github.com/amir1430/gamify/ledger"

	tb "github.com/tigerbeetle/tigerbeetle-go"
	"github.com/tigerbeetle/tigerbeetle-go/pkg/types"
)

// Driver adapts a TigerBeetle client to ledger.Driver. The client takes no context: calls block
// until TigerBeetle answers.
type Driver struct{ tb tb.Client }

func New(client tb.Client) *Driver { return &Driver{tb: client} }

func id(v ledger.ID) types.Uint128 {
	var b [16]byte
	binary.LittleEndian.PutUint64(b[:8], v.Lo)
	binary.LittleEndian.PutUint64(b[8:], v.Hi)
	return types.BytesToUint128(b)
}

func fromID(v types.Uint128) ledger.ID {
	b := v.Bytes()
	return ledger.ID{Hi: binary.LittleEndian.Uint64(b[8:]), Lo: binary.LittleEndian.Uint64(b[:8])}
}

// amountMax (2^128 - 1): since TigerBeetle 0.16 an amount of 0 moves 0, not "everything".
var amountMax = types.BytesToUint128([16]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff})

func (d *Driver) CreateAccounts(_ context.Context, accounts []ledger.Account) error {
	in := make([]types.Account, len(accounts))
	for i, a := range accounts {
		in[i] = types.Account{ID: id(a.ID), Ledger: a.Ledger, Code: a.Code,
			Flags: types.AccountFlags{DebitsMustNotExceedCredits: a.NoOverdraft}.ToUint16()}
	}
	res, err := d.tb.CreateAccounts(in)
	if err != nil {
		return err
	}
	for _, r := range res {
		if r.Result != types.AccountExists {
			return fmt.Errorf("tigerbeetle: create account %d: %s", r.Index, r.Result)
		}
	}
	return nil
}

func (d *Driver) CreateTransfers(_ context.Context, transfers []ledger.Transfer) ([]ledger.TransferResult, error) {
	in := make([]types.Transfer, len(transfers))
	for i, t := range transfers {
		amount := types.ToUint128(t.Amount)
		if t.Amount == ledger.AmountMax {
			amount = amountMax
		}
		in[i] = types.Transfer{ID: id(t.ID), DebitAccountID: id(t.Debit), CreditAccountID: id(t.Credit),
			Amount: amount, PendingID: id(t.PendingID), Ledger: t.Ledger, Code: t.Code,
			Flags: types.TransferFlags{Linked: t.Flags.Linked, Pending: t.Flags.Pending,
				PostPendingTransfer: t.Flags.PostPending, VoidPendingTransfer: t.Flags.VoidPending,
				BalancingDebit: t.Flags.BalancingDebit}.ToUint16()}
	}
	res, err := d.tb.CreateTransfers(in)
	if err != nil {
		return nil, err
	}
	out := make([]ledger.TransferResult, len(res))
	for i, r := range res {
		out[i] = ledger.TransferResult{Index: int(r.Index), Result: result(r.Result)}
	}
	return out, nil
}

// result maps the results the ledger acts on; the rest keep TigerBeetle's name.
func result(r types.CreateTransferResult) ledger.Result {
	switch r {
	case types.TransferOK:
		return ledger.ResultOK
	case types.TransferExists:
		return ledger.ResultExists
	case types.TransferIDAlreadyFailed:
		return ledger.ResultIDAlreadyFailed
	case types.TransferLinkedEventFailed:
		return ledger.ResultLinkedEventFailed
	case types.TransferLinkedEventChainOpen:
		return ledger.ResultLinkedChainOpen
	case types.TransferExceedsCredits:
		return ledger.ResultExceedsCredits
	case types.TransferDebitAccountNotFound, types.TransferCreditAccountNotFound:
		return ledger.ResultAccountNotFound
	case types.TransferPendingTransferNotFound:
		return ledger.ResultPendingNotFound
	case types.TransferPendingTransferNotPending:
		return ledger.ResultPendingNotPending
	case types.TransferPendingTransferAlreadyPosted:
		return ledger.ResultPendingAlreadyPosted
	case types.TransferPendingTransferAlreadyVoided:
		return ledger.ResultPendingAlreadyVoided
	case types.TransferExceedsPendingTransferAmount:
		return ledger.ResultExceedsPendingAmount
	case types.TransferExistsWithDifferentFlags, types.TransferExistsWithDifferentPendingID,
		types.TransferExistsWithDifferentTimeout, types.TransferExistsWithDifferentDebitAccountID,
		types.TransferExistsWithDifferentCreditAccountID, types.TransferExistsWithDifferentAmount,
		types.TransferExistsWithDifferentUserData128, types.TransferExistsWithDifferentUserData64,
		types.TransferExistsWithDifferentUserData32, types.TransferExistsWithDifferentLedger,
		types.TransferExistsWithDifferentCode:
		return ledger.ResultExistsDifferent
	}
	return ledger.Result(r.String())
}

func (d *Driver) LookupAccounts(_ context.Context, ids []ledger.ID) ([]ledger.AccountBalance, error) {
	in := make([]types.Uint128, len(ids))
	for i, v := range ids {
		in[i] = id(v)
	}
	accs, err := d.tb.LookupAccounts(in)
	if err != nil {
		return nil, err
	}
	out := make([]ledger.AccountBalance, len(accs))
	for i, a := range accs {
		b := ledger.AccountBalance{ID: fromID(a.ID), Ledger: a.Ledger}
		for _, f := range []struct {
			to *int64
			v  types.Uint128
		}{{&b.DebitsPending, a.DebitsPending}, {&b.DebitsPosted, a.DebitsPosted},
			{&b.CreditsPending, a.CreditsPending}, {&b.CreditsPosted, a.CreditsPosted}} {
			n := f.v.BigInt()
			if !n.IsInt64() {
				return nil, fmt.Errorf("tigerbeetle: account %v: total %s out of range", b.ID, n.String())
			}
			*f.to = n.Int64()
		}
		out[i] = b
	}
	return out, nil
}

// GetAccountTransfers is TigerBeetle's get_account_transfers (ledger.TransferReader). A filter that
// matches nothing returns none; TigerBeetle caps the limit at one batch.
func (d *Driver) GetAccountTransfers(_ context.Context, f ledger.AccountFilter) ([]ledger.StoredTransfer, error) {
	if f.Limit == 0 || (!f.Debits && !f.Credits) || (f.TimestampMax != 0 && f.TimestampMin > f.TimestampMax) {
		return nil, nil
	}
	ts, err := d.tb.GetAccountTransfers(types.AccountFilter{AccountID: id(f.AccountID), Code: f.Code,
		TimestampMin: f.TimestampMin, TimestampMax: f.TimestampMax, Limit: f.Limit,
		Flags: types.AccountFilterFlags{Debits: f.Debits, Credits: f.Credits, Reversed: f.Reversed}.ToUint32()})
	if err != nil {
		return nil, err
	}
	out := make([]ledger.StoredTransfer, len(ts))
	for i, t := range ts {
		n := t.Amount.BigInt()
		if !n.IsUint64() {
			return nil, fmt.Errorf("tigerbeetle: transfer %v: amount %s out of range", fromID(t.ID), n.String())
		}
		fl := t.TransferFlags()
		out[i] = ledger.StoredTransfer{Timestamp: t.Timestamp, Transfer: ledger.Transfer{ID: fromID(t.ID),
			Debit: fromID(t.DebitAccountID), Credit: fromID(t.CreditAccountID), Amount: n.Uint64(),
			Ledger: t.Ledger, Code: t.Code, PendingID: fromID(t.PendingID),
			Flags: ledger.Flags{Linked: fl.Linked, Pending: fl.Pending, PostPending: fl.PostPendingTransfer,
				VoidPending: fl.VoidPendingTransfer, BalancingDebit: fl.BalancingDebit}}}
	}
	return out, nil
}

// Ping checks TigerBeetle answers.
func (d *Driver) Ping(context.Context) error { return d.tb.Nop() }

// Dial connects to a cluster at "host:port,host:port". Close the driver when done.
func Dial(ctx context.Context, clusterID uint64, addresses string) (*Driver, error) {
	addrs, err := resolve(ctx, addresses)
	if err != nil {
		return nil, err
	}
	client, err := tb.NewClient(types.ToUint128(clusterID), addrs)
	if err != nil {
		return nil, err
	}
	return New(client), nil
}

func (d *Driver) Close() { d.tb.Close() }

// resolve turns host names into IPs, since TigerBeetle clients don't do DNS.
// ponytail: resolved once at startup; if the TigerBeetle container gets a new IP, restart the app
// (/readyz reports the ledger failing).
func resolve(ctx context.Context, list string) ([]string, error) {
	var out []string
	for _, a := range strings.Split(list, ",") {
		host, port, err := net.SplitHostPort(strings.TrimSpace(a))
		if err != nil {
			out = append(out, strings.TrimSpace(a)) // bare port like "3000"
			continue
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		ip := ips[0].IP
		for _, c := range ips { // prefer IPv4: docker networks are v4
			if c.IP.To4() != nil {
				ip = c.IP
				break
			}
		}
		out = append(out, net.JoinHostPort(ip.String(), port))
	}
	return out, nil
}
