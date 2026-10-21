package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/ViseonDev/ledger-core/internal/adapters/postgres"
	"github.com/ViseonDev/ledger-core/internal/app"
	"github.com/ViseonDev/ledger-core/internal/ports"
)

// readBody reads and size-limits the request body, returning the raw bytes so
// they can be fingerprinted for idempotency as well as decoded.
func readBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	limited := http.MaxBytesReader(w, r.Body, maxRequestBody)

	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("%w: reading body: %w", errBadRequest, err)
	}
	return body, nil
}

func decodeJSON[T any](body []byte) (T, error) {
	var value T

	decoder := json.NewDecoder(bytes.NewReader(body))
	// Unknown fields are an error rather than being ignored. A client that
	// misspells a field name should be told, not silently given a transaction
	// with missing fields.
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&value); err != nil {
		return value, fmt.Errorf("%w: %w", errBadRequest, err)
	}
	return value, nil
}

// idempotencyRequest builds the idempotency descriptor from the request.
func idempotencyRequest(r *http.Request, endpoint string, body []byte) (postgres.IdempotentRequest, error) {
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		return postgres.IdempotentRequest{}, ports.ErrIdempotencyKeyRequired
	}
	return postgres.IdempotentRequest{Key: key, Endpoint: endpoint, Body: body}, nil
}

// writeOutcome renders a successful idempotent result, marking replays so a
// client can tell the difference without changing its parsing.
func writeOutcome[T any](w http.ResponseWriter, logger *slog.Logger, status int, outcome postgres.Outcome[T]) {
	if outcome.Replayed {
		w.Header().Set("Idempotent-Replayed", "true")
		// A replay is not a creation, so 200 rather than 201.
		status = http.StatusOK
	}
	writeJSON(w, logger, status, outcome.Value)
}

func (s *Server) handleOpenAccount(w http.ResponseWriter, r *http.Request) {
	body, err := readBody(w, r)
	if err != nil {
		writeProblem(w, r, s.logger, err)
		return
	}
	cmd, err := decodeJSON[app.OpenAccountCommand](body)
	if err != nil {
		writeProblem(w, r, s.logger, err)
		return
	}
	idem, err := idempotencyRequest(r, "POST /v1/accounts", body)
	if err != nil {
		writeProblem(w, r, s.logger, err)
		return
	}

	outcome, err := s.ledger.OpenAccount(r.Context(), idem, cmd)
	if err != nil {
		writeProblem(w, r, s.logger, err)
		return
	}
	writeOutcome(w, s.logger, http.StatusCreated, outcome)
}

func (s *Server) handlePostTransaction(w http.ResponseWriter, r *http.Request) {
	body, err := readBody(w, r)
	if err != nil {
		writeProblem(w, r, s.logger, err)
		return
	}
	cmd, err := decodeJSON[app.PostTransactionCommand](body)
	if err != nil {
		writeProblem(w, r, s.logger, err)
		return
	}
	idem, err := idempotencyRequest(r, "POST /v1/transactions", body)
	if err != nil {
		writeProblem(w, r, s.logger, err)
		return
	}

	outcome, err := s.ledger.PostTransaction(r.Context(), idem, cmd)
	if err != nil {
		s.metrics.ObservePosting("rejected")
		writeProblem(w, r, s.logger, err)
		return
	}

	if !outcome.Replayed {
		s.metrics.ObservePosting("posted")
		var total int64
		for _, e := range outcome.Value.Entries {
			if e.Direction == "DEBIT" {
				total += e.Amount
			}
		}
		s.metrics.ObservePostedAmount(outcome.Value.Currency, total)
	} else {
		s.metrics.ObservePosting("replayed")
	}

	writeOutcome(w, s.logger, http.StatusCreated, outcome)
}

func (s *Server) handleReverseTransaction(w http.ResponseWriter, r *http.Request) {
	body, err := readBody(w, r)
	if err != nil {
		writeProblem(w, r, s.logger, err)
		return
	}
	cmd, err := decodeJSON[app.ReverseTransactionCommand](body)
	if err != nil {
		writeProblem(w, r, s.logger, err)
		return
	}
	idem, err := idempotencyRequest(r, "POST /v1/transactions/{id}/reversal", body)
	if err != nil {
		writeProblem(w, r, s.logger, err)
		return
	}

	outcome, err := s.ledger.ReverseTransaction(r.Context(), idem, chi.URLParam(r, "id"), cmd)
	if err != nil {
		writeProblem(w, r, s.logger, err)
		return
	}
	writeOutcome(w, s.logger, http.StatusCreated, outcome)
}

func (s *Server) handleGetAccount(w http.ResponseWriter, r *http.Request) {
	view, err := s.ledger.AccountByCode(r.Context(), chi.URLParam(r, "code"))
	if err != nil {
		writeProblem(w, r, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, view)
}

func (s *Server) handleGetTransaction(w http.ResponseWriter, r *http.Request) {
	view, err := s.ledger.TransactionByID(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeProblem(w, r, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, view)
}

func (s *Server) handleGetBalance(w http.ResponseWriter, r *http.Request) {
	authoritative, err := strconv.ParseBool(r.URL.Query().Get("authoritative"))
	if err != nil {
		authoritative = false
	}

	view, err := s.ledger.Balance(r.Context(), chi.URLParam(r, "id"), authoritative)
	if err != nil {
		writeProblem(w, r, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, view)
}
