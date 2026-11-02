package integration

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/ViseonDev/ledger-core/internal/adapters/postgres"
	"github.com/ViseonDev/ledger-core/internal/adapters/uuidgen"
	"github.com/ViseonDev/ledger-core/internal/app"
	"github.com/ViseonDev/ledger-core/internal/platform/clock"
	ledgerv1 "github.com/ViseonDev/ledger-core/proto/ledger/v1"
)

const bufSize = 1024 * 1024

// newGRPCClient starts an in-memory gRPC server over a fresh database.
func newGRPCClient(t *testing.T) (ledgerv1.LedgerServiceClient, *postgres.Store) {
	t.Helper()

	store := newTestDB(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := app.NewLedger(store, uuidgen.New(), clock.New(), logger)

	return newGRPCOver(t, service), store
}

// withKey attaches an idempotency key to the outgoing context.
func withKey(ctx context.Context, key string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "idempotency-key", key)
}

func openAccountViaGRPC(t *testing.T, client ledgerv1.LedgerServiceClient, code string, accountType ledgerv1.AccountType, allowNegative bool) *ledgerv1.Account {
	t.Helper()

	account, err := client.OpenAccount(withKey(t.Context(), "open_"+code), &ledgerv1.OpenAccountRequest{
		Code:          code,
		Name:          code,
		Type:          accountType,
		Currency:      "EUR",
		AllowNegative: allowNegative,
	})
	require.NoError(t, err)
	return account
}

func TestGRPCPostTransaction(t *testing.T) {
	client, _ := newGRPCClient(t)
	ctx := t.Context()

	openAccountViaGRPC(t, client, "equity:retained", ledgerv1.AccountType_ACCOUNT_TYPE_EQUITY, true)
	wallet := openAccountViaGRPC(t, client, "liabilities:customer:wallet:cus_0001", ledgerv1.AccountType_ACCOUNT_TYPE_LIABILITY, false)

	tx, err := client.PostTransaction(withKey(ctx, "grpc_fund_1"), &ledgerv1.PostTransactionRequest{
		Reference: "opening_balance",
		Currency:  "EUR",
		Metadata:  map[string]string{"source": "grpc"},
		Entries: []*ledgerv1.EntryInput{
			{AccountCode: "equity:retained", Direction: ledgerv1.Direction_DIRECTION_DEBIT, Amount: 100_000},
			{AccountCode: "liabilities:customer:wallet:cus_0001", Direction: ledgerv1.Direction_DIRECTION_CREDIT, Amount: 100_000},
		},
	})
	require.NoError(t, err)

	assert.Equal(t, "opening_balance", tx.GetReference())
	assert.Equal(t, "EUR", tx.GetCurrency())
	assert.Len(t, tx.GetEntries(), 2)
	assert.NotNil(t, tx.GetPostedAt())
	assert.Equal(t, "grpc", tx.GetMetadata()["source"])

	balance, err := client.GetBalance(ctx, &ledgerv1.GetBalanceRequest{AccountId: wallet.GetId()})
	require.NoError(t, err)
	assert.Equal(t, int64(100_000), balance.GetBalance())
	assert.Equal(t, "EUR", balance.GetCurrency())
}

func TestGRPCRejectsUnbalanced(t *testing.T) {
	client, _ := newGRPCClient(t)

	openAccountViaGRPC(t, client, "equity:retained", ledgerv1.AccountType_ACCOUNT_TYPE_EQUITY, true)
	openAccountViaGRPC(t, client, "liabilities:settlement:pending", ledgerv1.AccountType_ACCOUNT_TYPE_LIABILITY, true)

	_, err := client.PostTransaction(withKey(t.Context(), "grpc_bad_1"), &ledgerv1.PostTransactionRequest{
		Currency: "EUR",
		Entries: []*ledgerv1.EntryInput{
			{AccountCode: "equity:retained", Direction: ledgerv1.Direction_DIRECTION_DEBIT, Amount: 25_000},
			{AccountCode: "liabilities:settlement:pending", Direction: ledgerv1.Direction_DIRECTION_CREDIT, Amount: 24_000},
		},
	})

	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
	assert.Contains(t, status.Convert(err).Message(), "25000")
}

func TestGRPCRequiresIdempotencyKey(t *testing.T) {
	client, _ := newGRPCClient(t)

	_, err := client.OpenAccount(t.Context(), &ledgerv1.OpenAccountRequest{
		Code: "equity:retained", Name: "x", Type: ledgerv1.AccountType_ACCOUNT_TYPE_EQUITY, Currency: "EUR",
	})

	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestGRPCReplaysIdempotentRequest(t *testing.T) {
	client, _ := newGRPCClient(t)
	ctx := t.Context()

	openAccountViaGRPC(t, client, "equity:retained", ledgerv1.AccountType_ACCOUNT_TYPE_EQUITY, true)
	openAccountViaGRPC(t, client, "liabilities:settlement:pending", ledgerv1.AccountType_ACCOUNT_TYPE_LIABILITY, true)

	request := &ledgerv1.PostTransactionRequest{
		Reference: "grpc_replay",
		Currency:  "EUR",
		Entries: []*ledgerv1.EntryInput{
			{AccountCode: "equity:retained", Direction: ledgerv1.Direction_DIRECTION_DEBIT, Amount: 700},
			{AccountCode: "liabilities:settlement:pending", Direction: ledgerv1.Direction_DIRECTION_CREDIT, Amount: 700},
		},
	}

	first, err := client.PostTransaction(withKey(ctx, "grpc_same"), request)
	require.NoError(t, err)

	second, err := client.PostTransaction(withKey(ctx, "grpc_same"), request)
	require.NoError(t, err)

	assert.Equal(t, first.GetId(), second.GetId(), "a replay must return the original transaction")
}

// An UNSPECIFIED enum must be rejected, not silently defaulted. This is the
// protobuf equivalent of Direction(0) being invalid in the domain.
func TestGRPCRejectsUnspecifiedEnums(t *testing.T) {
	client, _ := newGRPCClient(t)

	_, err := client.OpenAccount(withKey(t.Context(), "unspecified_type"), &ledgerv1.OpenAccountRequest{
		Code:     "equity:retained",
		Name:     "Retained",
		Type:     ledgerv1.AccountType_ACCOUNT_TYPE_UNSPECIFIED,
		Currency: "EUR",
	})

	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestGRPCNotFound(t *testing.T) {
	client, _ := newGRPCClient(t)

	_, err := client.GetAccount(t.Context(), &ledgerv1.GetAccountRequest{Code: "equity:missing"})

	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

// Same operations over REST and gRPC should end up in the same journal.
func TestBothTransportsAgree(t *testing.T) {
	// One database, two transports over it.
	store := newTestDB(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := app.NewLedger(store, uuidgen.New(), clock.New(), logger)

	httpServer := newAPIOver(t, service, store)
	grpcClient := newGRPCOver(t, service)

	ctx := t.Context()

	// Open the accounts over gRPC.
	_, err := grpcClient.OpenAccount(withKey(ctx, "agree_equity"), &ledgerv1.OpenAccountRequest{
		Code: "equity:retained", Name: "Retained", Type: ledgerv1.AccountType_ACCOUNT_TYPE_EQUITY,
		Currency: "EUR", AllowNegative: true,
	})
	require.NoError(t, err)

	_, err = grpcClient.OpenAccount(withKey(ctx, "agree_wallet"), &ledgerv1.OpenAccountRequest{
		Code: "liabilities:customer:wallet:cus_0001", Name: "Wallet",
		Type: ledgerv1.AccountType_ACCOUNT_TYPE_LIABILITY, Currency: "EUR",
	})
	require.NoError(t, err)

	// Post one transaction over REST.
	restResp := doRequest(t, httpServer, "POST", "/v1/transactions", "agree_rest", app.PostTransactionCommand{
		Reference: "via_rest",
		Currency:  "EUR",
		Entries: []app.EntryInput{
			{AccountCode: "equity:retained", Direction: "DEBIT", Amount: 30_000},
			{AccountCode: "liabilities:customer:wallet:cus_0001", Direction: "CREDIT", Amount: 30_000},
		},
	})
	require.Equal(t, 201, restResp.status, "body: %s", restResp.body)

	var restView app.TransactionView
	restResp.decode(t, &restView)

	// Post an equivalent transaction over gRPC.
	grpcTx, err := grpcClient.PostTransaction(withKey(ctx, "agree_grpc"), &ledgerv1.PostTransactionRequest{
		Reference: "via_grpc",
		Currency:  "EUR",
		Entries: []*ledgerv1.EntryInput{
			{AccountCode: "equity:retained", Direction: ledgerv1.Direction_DIRECTION_DEBIT, Amount: 30_000},
			{AccountCode: "liabilities:customer:wallet:cus_0001", Direction: ledgerv1.Direction_DIRECTION_CREDIT, Amount: 30_000},
		},
	})
	require.NoError(t, err)

	// Read the REST-created transaction back over gRPC and compare.
	viaGRPC, err := grpcClient.GetTransaction(ctx, &ledgerv1.GetTransactionRequest{Id: restView.ID})
	require.NoError(t, err)

	assert.Equal(t, restView.ID, viaGRPC.GetId())
	assert.Equal(t, restView.Reference, viaGRPC.GetReference())
	assert.Equal(t, restView.Currency, viaGRPC.GetCurrency())
	assert.Len(t, viaGRPC.GetEntries(), len(restView.Entries))

	// Read the gRPC-created transaction back over REST and compare.
	viaREST := doRequest(t, httpServer, "GET", "/v1/transactions/"+grpcTx.GetId(), "", nil)
	require.Equal(t, 200, viaREST.status)

	var grpcAsREST app.TransactionView
	viaREST.decode(t, &grpcAsREST)
	assert.Equal(t, grpcTx.GetId(), grpcAsREST.ID)
	assert.Equal(t, "via_grpc", grpcAsREST.Reference)

	// Both transports report the same balance: 30000 + 30000.
	restBalance := doRequest(t, httpServer, "GET",
		"/v1/accounts/"+viaGRPC.GetEntries()[1].GetAccountId()+"/balance", "", nil)
	var balanceView app.BalanceView
	restBalance.decode(t, &balanceView)

	grpcBalance, err := grpcClient.GetBalance(ctx, &ledgerv1.GetBalanceRequest{
		AccountId: viaGRPC.GetEntries()[1].GetAccountId(),
	})
	require.NoError(t, err)

	assert.Equal(t, int64(60_000), balanceView.Balance)
	assert.Equal(t, balanceView.Balance, grpcBalance.GetBalance(),
		"the two transports disagree about the balance")
}
