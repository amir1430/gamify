package ledger

import "context"

// Driver is a double-entry store with TigerBeetle's semantics: the ledger's money logic (game operations,
// holds, pots, swaps) is written once against it, and every driver must pass ledgertest.Run.
//
// The contract, as TigerBeetle defines it:
//   - IDs are chosen by the caller and unique forever: a transfer whose ID exists with the same
//     fields returns ResultExists (a retry); one that failed for a reason that depends on state
//     (ResultExceedsCredits, ResultAccountNotFound, ResultPendingNotFound) reserves its ID, and every
//     later try returns ResultIDAlreadyFailed.
//   - A transfer with Linked chains to the next one; a chain applies whole or not at all. The event
//     that broke it gets its own result, the others ResultLinkedEventFailed.
//   - Transfers without Linked are independent: each applies or fails on its own.
//   - NoOverdraft accounts refuse a debit that takes posted + pending debits above posted credits.
type Driver interface {
	// CreateAccounts creates accounts; one that exists with the same fields is not an error.
	CreateAccounts(ctx context.Context, accounts []Account) error
	// CreateTransfers applies the transfers in order and returns the result of every one that is
	// not ResultOK. An error means the call itself failed and nothing is known about the batch.
	CreateTransfers(ctx context.Context, transfers []Transfer) ([]TransferResult, error)
	// LookupAccounts returns the accounts found, in no particular order; missing IDs are left out.
	LookupAccounts(ctx context.Context, ids []ID) ([]AccountBalance, error)
}

// ID is a 128-bit account or transfer ID.
type ID struct{ Hi, Lo uint64 }

// Account is created once; balances only change through transfers.
type Account struct {
	ID          ID
	Ledger      uint32 // one per currency; transfers never cross ledgers
	Code        uint16 // what the account is for (user, house, pot, …)
	NoOverdraft bool   // debits may never exceed credits (users and pots)
}

// AccountBalance is an account's running totals. Spendable = CreditsPosted - DebitsPosted - DebitsPending.
type AccountBalance struct {
	ID                                                         ID
	Ledger                                                     uint32
	DebitsPending, DebitsPosted, CreditsPending, CreditsPosted int64
}

// Transfer moves Amount from Debit to Credit on Ledger.
type Transfer struct {
	ID, Debit, Credit ID
	Amount            uint64 // AmountMax with BalancingDebit: as much as the debit account allows
	Ledger            uint32
	Code              uint16
	PendingID         ID // the pending transfer a PostPending or VoidPending finishes
	Flags             Flags
}

// AmountMax asks a balancing transfer for everything the account allows.
const AmountMax = ^uint64(0)

// Flags of a transfer.
type Flags struct {
	Linked         bool // chain with the next transfer
	Pending        bool // reserve the amount until posted or voided (no timeout)
	PostPending    bool // move a pending transfer's amount for good
	VoidPending    bool // release a pending transfer
	BalancingDebit bool // debit at most what the debit account can spend
}

// TransferResult is the outcome of transfer Index of a batch.
type TransferResult struct {
	Index  int
	Result Result
}

// Result names why a transfer did not apply. Drivers return these for the cases the ledger acts on;
// anything else is passed through with the driver's own name (an error to the caller).
type Result string

const (
	ResultOK                   Result = "ok"
	ResultExists               Result = "exists" // same ID, same fields: applied by an earlier try
	ResultExistsDifferent      Result = "exists_with_different_fields"
	ResultIDAlreadyFailed      Result = "id_already_failed"
	ResultLinkedEventFailed    Result = "linked_event_failed"
	ResultLinkedChainOpen      Result = "linked_event_chain_open"
	ResultExceedsCredits       Result = "exceeds_credits"
	ResultAccountNotFound      Result = "account_not_found"
	ResultLedgerMismatch       Result = "ledger_mismatch"
	ResultPendingNotFound      Result = "pending_transfer_not_found"
	ResultPendingNotPending    Result = "pending_transfer_not_pending"
	ResultPendingAlreadyPosted Result = "pending_transfer_already_posted"
	ResultPendingAlreadyVoided Result = "pending_transfer_already_voided"
	ResultPendingDifferent     Result = "pending_transfer_has_different_fields"
	ResultExceedsPendingAmount Result = "exceeds_pending_transfer_amount"
	ResultOverflow             Result = "overflow"
	ResultInvalid              Result = "invalid"
)
