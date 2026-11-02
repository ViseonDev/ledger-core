package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ViseonDev/ledger-core/internal/adapters/uuidgen"
	"github.com/ViseonDev/ledger-core/internal/app"
	"github.com/ViseonDev/ledger-core/internal/platform/clock"
)

// newAPI builds a full HTTP stack over a fresh database.
func newAPI(t *testing.T) *httptest.Server {
	t.Helper()

	store := newTestDB(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := app.NewLedger(store, uuidgen.New(), clock.New(), logger)

	return newAPIOver(t, service, store)
}

type apiResponse struct {
	status  int
	headers http.Header
	body    []byte
}

func (r apiResponse) decode(t *testing.T, into any) {
	t.Helper()
	require.NoError(t, json.Unmarshal(r.body, into), "body was: %s", r.body)
}

func doRequest(t *testing.T, server *httptest.Server, method, path, idempotencyKey string, body any) apiResponse {
	t.Helper()

	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		require.NoError(t, err)
	}

	req, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, bytes.NewReader(payload))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}

	resp, err := server.Client().Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	responseBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	return apiResponse{status: resp.StatusCode, headers: resp.Header, body: responseBody}
}

func openAccountViaAPI(t *testing.T, server *httptest.Server, code, accountType string, allowNegative bool) app.AccountView {
	t.Helper()

	resp := doRequest(t, server, http.MethodPost, "/v1/accounts", "open_"+code, app.OpenAccountCommand{
		Code:          code,
		Name:          code,
		Type:          accountType,
		Currency:      "EUR",
		AllowNegative: allowNegative,
	})
	require.Equal(t, http.StatusCreated, resp.status, "body: %s", resp.body)

	var view app.AccountView
	resp.decode(t, &view)
	return view
}

func TestAPIPostTransaction(t *testing.T) {
	server := newAPI(t)

	openAccountViaAPI(t, server, "equity:retained", "EQUITY", true)
	wallet := openAccountViaAPI(t, server, "liabilities:customer:wallet:cus_0001", "LIABILITY", false)
	openAccountViaAPI(t, server, "liabilities:settlement:pending", "LIABILITY", true)

	// Fund the customer wallet from retained equity.
	fund := doRequest(t, server, http.MethodPost, "/v1/transactions", "fund_0001", app.PostTransactionCommand{
		Reference: "opening_balance",
		Currency:  "EUR",
		Entries: []app.EntryInput{
			{AccountCode: "equity:retained", Direction: "DEBIT", Amount: 100_000},
			{AccountCode: "liabilities:customer:wallet:cus_0001", Direction: "CREDIT", Amount: 100_000},
		},
	})
	require.Equal(t, http.StatusCreated, fund.status, "body: %s", fund.body)

	// Spend from the wallet towards settlement.
	pay := doRequest(t, server, http.MethodPost, "/v1/transactions", "pay_4471", app.PostTransactionCommand{
		Reference: "order_4471",
		Currency:  "EUR",
		Metadata:  map[string]string{"source": "checkout"},
		Entries: []app.EntryInput{
			{AccountCode: "liabilities:customer:wallet:cus_0001", Direction: "DEBIT", Amount: 25_000},
			{AccountCode: "liabilities:settlement:pending", Direction: "CREDIT", Amount: 25_000},
		},
	})
	require.Equal(t, http.StatusCreated, pay.status, "body: %s", pay.body)

	var view app.TransactionView
	pay.decode(t, &view)
	assert.Equal(t, "order_4471", view.Reference)
	assert.Equal(t, "EUR", view.Currency)
	assert.Len(t, view.Entries, 2)
	assert.Equal(t, "checkout", view.Metadata["source"])

	// The cached balance reflects the spending.
	balanceResp := doRequest(t, server, http.MethodGet, "/v1/accounts/"+wallet.ID+"/balance", "", nil)
	require.Equal(t, http.StatusOK, balanceResp.status)

	var balance app.BalanceView
	balanceResp.decode(t, &balance)
	assert.Equal(t, int64(75_000), balance.Balance)
	assert.False(t, balance.Authoritative)

	// Authoritative balance recalculation from journal agrees with cache.
	authResp := doRequest(t, server, http.MethodGet, "/v1/accounts/"+wallet.ID+"/balance?authoritative=true", "", nil)
	require.Equal(t, http.StatusOK, authResp.status)

	var authoritative app.BalanceView
	authResp.decode(t, &authoritative)
	assert.Equal(t, balance.Balance, authoritative.Balance, "cache and journal disagree")
	assert.True(t, authoritative.Authoritative)
}

func TestAPIRejectsUnbalancedTransaction(t *testing.T) {
	server := newAPI(t)
	openAccountViaAPI(t, server, "equity:retained", "EQUITY", true)
	openAccountViaAPI(t, server, "liabilities:settlement:pending", "LIABILITY", true)

	resp := doRequest(t, server, http.MethodPost, "/v1/transactions", "bad_1", app.PostTransactionCommand{
		Currency: "EUR",
		Entries: []app.EntryInput{
			{AccountCode: "equity:retained", Direction: "DEBIT", Amount: 25_000},
			{AccountCode: "liabilities:settlement:pending", Direction: "CREDIT", Amount: 24_000},
		},
	})

	assert.Equal(t, http.StatusUnprocessableEntity, resp.status)
	assert.Equal(t, "application/problem+json", resp.headers.Get("Content-Type"))

	var problem map[string]any
	resp.decode(t, &problem)
	assert.Equal(t, "https://ledger.local/problems/unbalanced-transaction", problem["type"])
	assert.Equal(t, "Transaction does not balance", problem["title"])
	assert.Contains(t, problem["detail"], "25000")
	assert.Contains(t, problem["detail"], "24000")
	assert.NotEmpty(t, problem["request_id"])
	assert.NotEmpty(t, resp.headers.Get("X-Request-Id"))
}

func TestAPIRequiresIdempotencyKey(t *testing.T) {
	server := newAPI(t)

	resp := doRequest(t, server, http.MethodPost, "/v1/transactions", "", app.PostTransactionCommand{
		Currency: "EUR",
	})

	assert.Equal(t, http.StatusPreconditionRequired, resp.status)

	var problem map[string]any
	resp.decode(t, &problem)
	assert.Equal(t, "https://ledger.local/problems/idempotency-key-required", problem["type"])
}

func TestAPIReplaysIdempotentRequest(t *testing.T) {
	server := newAPI(t)
	openAccountViaAPI(t, server, "equity:retained", "EQUITY", true)
	openAccountViaAPI(t, server, "liabilities:settlement:pending", "LIABILITY", true)

	command := app.PostTransactionCommand{
		Reference: "order_9",
		Currency:  "EUR",
		Entries: []app.EntryInput{
			{AccountCode: "equity:retained", Direction: "DEBIT", Amount: 500},
			{AccountCode: "liabilities:settlement:pending", Direction: "CREDIT", Amount: 500},
		},
	}

	first := doRequest(t, server, http.MethodPost, "/v1/transactions", "same_key", command)
	require.Equal(t, http.StatusCreated, first.status)
	assert.Empty(t, first.headers.Get("Idempotent-Replayed"))

	second := doRequest(t, server, http.MethodPost, "/v1/transactions", "same_key", command)
	require.Equal(t, http.StatusOK, second.status, "a replay is not a creation")
	assert.Equal(t, "true", second.headers.Get("Idempotent-Replayed"))

	var a, b app.TransactionView
	first.decode(t, &a)
	second.decode(t, &b)
	assert.Equal(t, a.ID, b.ID)
}

func TestAPIRejectsUnknownFields(t *testing.T) {
	server := newAPI(t)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		server.URL+"/v1/transactions",
		bytes.NewReader([]byte(`{"currency":"EUR","unexpected_field":"extra","entries":[]}`)))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "typo_key")

	resp, err := server.Client().Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode,
		"a misspelled field must be reported, not silently ignored")
}

func TestAPIHealthAndReadiness(t *testing.T) {
	server := newAPI(t)

	health := doRequest(t, server, http.MethodGet, "/healthz", "", nil)
	assert.Equal(t, http.StatusOK, health.status)

	ready := doRequest(t, server, http.MethodGet, "/readyz", "", nil)
	assert.Equal(t, http.StatusOK, ready.status)
}

func TestAPIMetricsAreExposed(t *testing.T) {
	server := newAPI(t)
	openAccountViaAPI(t, server, "equity:retained", "EQUITY", true)

	metrics := doRequest(t, server, http.MethodGet, "/metrics", "", nil)
	require.Equal(t, http.StatusOK, metrics.status)

	body := string(metrics.body)
	assert.Contains(t, body, "ledger_http_requests_total")
	assert.Contains(t, body, "ledger_http_request_duration_seconds")

	// Route label should be the pattern, not the concrete path, so time series stay bounded.
	assert.NotContains(t, body, `route="/v1/accounts/liabilities:customer:wallet:cus_0001"`)
}

func TestAPIReversal(t *testing.T) {
	server := newAPI(t)
	openAccountViaAPI(t, server, "equity:retained", "EQUITY", true)
	wallet := openAccountViaAPI(t, server, "liabilities:customer:wallet:cus_0001", "LIABILITY", true)

	posted := doRequest(t, server, http.MethodPost, "/v1/transactions", "mistake_1", app.PostTransactionCommand{
		Reference: "oops",
		Currency:  "EUR",
		Entries: []app.EntryInput{
			{AccountCode: "equity:retained", Direction: "DEBIT", Amount: 5_000},
			{AccountCode: "liabilities:customer:wallet:cus_0001", Direction: "CREDIT", Amount: 5_000},
		},
	})
	require.Equal(t, http.StatusCreated, posted.status, "body: %s", posted.body)

	var original app.TransactionView
	posted.decode(t, &original)

	reversal := doRequest(t, server, http.MethodPost,
		"/v1/transactions/"+original.ID+"/reversal", "reverse_1",
		app.ReverseTransactionCommand{Reason: "posted in error"})
	require.Equal(t, http.StatusCreated, reversal.status, "body: %s", reversal.body)

	var reversed app.TransactionView
	reversal.decode(t, &reversed)
	assert.Equal(t, original.ID, reversed.ReversalOf)

	// Balance is back to zero.
	balanceResp := doRequest(t, server, http.MethodGet, "/v1/accounts/"+wallet.ID+"/balance", "", nil)
	var balance app.BalanceView
	balanceResp.decode(t, &balance)
	assert.Equal(t, int64(0), balance.Balance)

	// A second reversal is a conflict.
	again := doRequest(t, server, http.MethodPost,
		"/v1/transactions/"+original.ID+"/reversal", "reverse_2",
		app.ReverseTransactionCommand{Reason: "again"})
	assert.Equal(t, http.StatusConflict, again.status)
}
