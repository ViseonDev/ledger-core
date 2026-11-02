package psp

import (
	"context"
	"fmt"
	"sync"
)

// Simulator is an in-memory payment processor for development and testing.
type Simulator struct {
	mu sync.Mutex

	// results memoizes responses by idempotency key.
	results map[string]any

	// failures maps a step name to the error the next call returns.
	// Tests use this to simulate network hiccups or declines.
	failures map[string]error

	authCounter    int
	captureCounter int

	// Voided and Refunded record compensation calls so tests can assert undo actions.
	Voided   []string
	Refunded []string
}

// NewSimulator returns a processor that succeeds on every call by default.
func NewSimulator() *Simulator {
	return &Simulator{
		results:  make(map[string]any),
		failures: make(map[string]error),
	}
}

// FailStep causes the next call for the given step to return err. Nil clears the error.
func (s *Simulator) FailStep(step string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err == nil {
		delete(s.failures, step)
		return
	}
	s.failures[step] = err
}

// Authorize implements Processor.
func (s *Simulator) Authorize(_ context.Context, req AuthorizeRequest) (AuthorizeResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if cached, ok := s.results[req.IdempotencyKey]; ok {
		if resp, isAuth := cached.(AuthorizeResponse); isAuth {
			return resp, nil
		}
	}
	if err := s.take("authorize"); err != nil {
		return AuthorizeResponse{}, err
	}

	s.authCounter++
	response := AuthorizeResponse{AuthorizationID: fmt.Sprintf("auth_sim_%06d", s.authCounter)}
	s.results[req.IdempotencyKey] = response
	return response, nil
}

// Capture implements Processor.
func (s *Simulator) Capture(_ context.Context, idempotencyKey, authorizationID string, amountMinor int64) (CaptureResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if cached, ok := s.results[idempotencyKey]; ok {
		if resp, isCap := cached.(CaptureResponse); isCap {
			return resp, nil
		}
	}
	if err := s.take("capture"); err != nil {
		return CaptureResponse{}, err
	}
	if authorizationID == "" {
		return CaptureResponse{}, Declinedf("capture without an authorization")
	}
	if amountMinor <= 0 {
		return CaptureResponse{}, Declinedf("capture amount must be positive, got %d", amountMinor)
	}

	s.captureCounter++
	response := CaptureResponse{CaptureID: fmt.Sprintf("cap_sim_%06d", s.captureCounter)}
	s.results[idempotencyKey] = response
	return response, nil
}

// Void implements Processor.
func (s *Simulator) Void(_ context.Context, idempotencyKey, authorizationID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.results[idempotencyKey]; ok {
		return nil
	}
	if err := s.take("void"); err != nil {
		return err
	}

	s.Voided = append(s.Voided, authorizationID)
	s.results[idempotencyKey] = struct{}{}
	return nil
}

// Refund implements Processor.
func (s *Simulator) Refund(_ context.Context, idempotencyKey, captureID string, amountMinor int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.results[idempotencyKey]; ok {
		return nil
	}
	if err := s.take("refund"); err != nil {
		return err
	}

	s.Refunded = append(s.Refunded, captureID)
	s.results[idempotencyKey] = struct{}{}
	return nil
}

// take retrieves and consumes a configured failure for a step, if set.
func (s *Simulator) take(step string) error {
	err, ok := s.failures[step]
	if !ok {
		return nil
	}
	delete(s.failures, step)
	return err
}
