package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/ViseonDev/ledger-core/internal/domain/ledger"
	"github.com/ViseonDev/ledger-core/internal/domain/money"
	"github.com/ViseonDev/ledger-core/internal/ports"
)

// problemBaseURI prefixes problem types. They are ids, they don't need to resolve.
const problemBaseURI = "https://ledger.local/problems/"

// Problem is an RFC 9457 problem details document.
type Problem struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail,omitempty"`
	Instance string `json:"instance,omitempty"`

	// RequestID is an extension member, matches the logs.
	RequestID string `json:"request_id,omitempty"`
}

// problemFor maps an error to a problem document.
func problemFor(err error) Problem {
	switch {
	// 422: the request was understood but violates a rule
	case errors.Is(err, ledger.ErrUnbalanced):
		return Problem{Type: problemBaseURI + "unbalanced-transaction",
			Title: "Transaction does not balance", Status: http.StatusUnprocessableEntity}

	case errors.Is(err, ledger.ErrInsufficientFunds):
		return Problem{Type: problemBaseURI + "insufficient-funds",
			Title: "Insufficient funds", Status: http.StatusUnprocessableEntity}

	case errors.Is(err, ledger.ErrAccountClosed):
		return Problem{Type: problemBaseURI + "account-closed",
			Title: "Account is closed", Status: http.StatusUnprocessableEntity}

	case errors.Is(err, money.ErrCurrencyMismatch):
		return Problem{Type: problemBaseURI + "currency-mismatch",
			Title: "Currency mismatch", Status: http.StatusUnprocessableEntity}

	case errors.Is(err, ledger.ErrTooFewEntries),
		errors.Is(err, ledger.ErrNonPositiveAmount),
		errors.Is(err, ledger.ErrReversalReasonRequired),
		errors.Is(err, ledger.ErrInvalidMetadata),
		errors.Is(err, ledger.ErrInvalidPostedAt):
		return Problem{Type: problemBaseURI + "invalid-transaction",
			Title: "Invalid transaction", Status: http.StatusUnprocessableEntity}

	// 400: the request itself is malformed
	case errors.Is(err, money.ErrInvalidCurrency),
		errors.Is(err, ledger.ErrInvalidDirection),
		errors.Is(err, ledger.ErrInvalidAccountType),
		errors.Is(err, ledger.ErrInvalidAccountCode),
		errors.Is(err, ledger.ErrInvalidAccountName),
		errors.Is(err, ledger.ErrAccountCodeTypeMismatch),
		errors.Is(err, ledger.ErrInvalidID),
		errors.Is(err, errBadRequest):
		return Problem{Type: problemBaseURI + "invalid-request",
			Title: "Invalid request", Status: http.StatusBadRequest}

	// 404
	case errors.Is(err, ledger.ErrAccountNotFound):
		return Problem{Type: problemBaseURI + "account-not-found",
			Title: "Account not found", Status: http.StatusNotFound}

	case errors.Is(err, ledger.ErrTransactionNotFound):
		return Problem{Type: problemBaseURI + "transaction-not-found",
			Title: "Transaction not found", Status: http.StatusNotFound}

	// 409: state conflict
	case errors.Is(err, ports.ErrIdempotencyConflict):
		return Problem{Type: problemBaseURI + "idempotency-conflict",
			Title: "Idempotency key reused with a different request", Status: http.StatusConflict}

	case errors.Is(err, ledger.ErrAlreadyReversed):
		return Problem{Type: problemBaseURI + "already-reversed",
			Title: "Transaction has already been reversed", Status: http.StatusConflict}

	case errors.Is(err, ledger.ErrAccountExists):
		return Problem{Type: problemBaseURI + "account-exists",
			Title: "Account already exists", Status: http.StatusConflict}

	case errors.Is(err, ledger.ErrTransactionExists):
		return Problem{Type: problemBaseURI + "transaction-exists",
			Title: "Transaction already posted", Status: http.StatusConflict}

	// 428: a required precondition is missing
	case errors.Is(err, ports.ErrIdempotencyKeyRequired),
		errors.Is(err, ports.ErrIdempotencyKeyTooLong):
		return Problem{Type: problemBaseURI + "idempotency-key-required",
			Title: "An Idempotency-Key header is required", Status: http.StatusPreconditionRequired}

	// 500
	default:
		return Problem{Type: problemBaseURI + "internal",
			Title: "Internal server error", Status: http.StatusInternalServerError}
	}
}

// writeProblem renders an error as a problem document.
func writeProblem(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error) {
	problem := problemFor(err)
	problem.Instance = r.URL.Path
	problem.RequestID = requestIDFrom(r.Context())

	if problem.Status >= http.StatusInternalServerError {
		logger.ErrorContext(r.Context(), "request failed",
			slog.String("error", err.Error()),
			slog.String("path", r.URL.Path),
			slog.String("method", r.Method),
		)
	} else {
		problem.Detail = err.Error()
		logger.InfoContext(r.Context(), "request rejected",
			slog.String("error", err.Error()),
			slog.Int("status", problem.Status),
			slog.String("path", r.URL.Path),
		)
	}

	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(problem.Status)
	if err := json.NewEncoder(w).Encode(problem); err != nil {
		logger.ErrorContext(r.Context(), "writing problem response", slog.String("error", err.Error()))
	}
}

// writeJSON renders a successful response.
func writeJSON(w http.ResponseWriter, logger *slog.Logger, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		logger.Error("writing response body", slog.String("error", err.Error()))
	}
}
