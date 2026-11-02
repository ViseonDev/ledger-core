// Package grpcapi serves the ledger over gRPC.
package grpcapi

import (
	"context"
	"log/slog"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/ViseonDev/ledger-core/internal/adapters/postgres"
	"github.com/ViseonDev/ledger-core/internal/app"
	ledgerv1 "github.com/ViseonDev/ledger-core/proto/ledger/v1"
)

// Server implements ledgerv1.LedgerServiceServer.
type Server struct {
	// Required by protoc-gen-go-grpc.
	ledgerv1.UnimplementedLedgerServiceServer

	ledger *app.Ledger
	logger *slog.Logger
}

// NewServer builds the gRPC service.
func NewServer(ledgerService *app.Ledger, logger *slog.Logger) *Server {
	return &Server{ledger: ledgerService, logger: logger}
}

// Register wires the service and its interceptors into a gRPC server.
func Register(logger *slog.Logger, ledgerService *app.Ledger) *grpc.Server {
	server := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			loggingInterceptor(logger),
			errorInterceptor(logger),
		),
	)
	ledgerv1.RegisterLedgerServiceServer(server, NewServer(ledgerService, logger))
	return server
}

// idempotentRequest builds the idempotency descriptor for a mutating RPC.
func idempotentRequest(ctx context.Context, endpoint string, message proto.Message) (postgres.IdempotentRequest, error) {
	key, keyErr := idempotencyKeyFrom(ctx)
	if keyErr != nil {
		return postgres.IdempotentRequest{}, keyErr
	}

	// Fingerprint expects JSON, so encode the message as JSON rather than wire bytes.
	body, marshalErr := protojson.MarshalOptions{UseProtoNames: true}.Marshal(message)
	if marshalErr != nil {
		return postgres.IdempotentRequest{}, marshalErr
	}
	return postgres.IdempotentRequest{Key: key, Endpoint: endpoint, Body: body}, nil
}

// OpenAccount implements ledgerv1.LedgerServiceServer.
func (s *Server) OpenAccount(ctx context.Context, req *ledgerv1.OpenAccountRequest) (*ledgerv1.Account, error) {
	idem, err := idempotentRequest(ctx, "grpc:OpenAccount", req)
	if err != nil {
		return nil, err
	}

	outcome, err := s.ledger.OpenAccount(ctx, idem, app.OpenAccountCommand{
		Code:          req.GetCode(),
		Name:          req.GetName(),
		Type:          accountTypeFromProto(req.GetType()),
		Currency:      req.GetCurrency(),
		AllowNegative: req.GetAllowNegative(),
	})
	if err != nil {
		return nil, err
	}
	return accountToProto(outcome.Value), nil
}

// GetAccount implements ledgerv1.LedgerServiceServer.
func (s *Server) GetAccount(ctx context.Context, req *ledgerv1.GetAccountRequest) (*ledgerv1.Account, error) {
	view, err := s.ledger.AccountByCode(ctx, req.GetCode())
	if err != nil {
		return nil, err
	}
	return accountToProto(view), nil
}

// GetBalance implements ledgerv1.LedgerServiceServer.
func (s *Server) GetBalance(ctx context.Context, req *ledgerv1.GetBalanceRequest) (*ledgerv1.Balance, error) {
	view, err := s.ledger.Balance(ctx, req.GetAccountId(), req.GetAuthoritative())
	if err != nil {
		return nil, err
	}
	return balanceToProto(view), nil
}

// PostTransaction implements ledgerv1.LedgerServiceServer.
func (s *Server) PostTransaction(ctx context.Context, req *ledgerv1.PostTransactionRequest) (*ledgerv1.Transaction, error) {
	idem, err := idempotentRequest(ctx, "grpc:PostTransaction", req)
	if err != nil {
		return nil, err
	}

	outcome, err := s.ledger.PostTransaction(ctx, idem, app.PostTransactionCommand{
		Reference: req.GetReference(),
		Currency:  req.GetCurrency(),
		Metadata:  req.GetMetadata(),
		Entries:   entryInputsFromProto(req.GetEntries()),
	})
	if err != nil {
		return nil, err
	}
	return transactionToProto(outcome.Value), nil
}

// GetTransaction implements ledgerv1.LedgerServiceServer.
func (s *Server) GetTransaction(ctx context.Context, req *ledgerv1.GetTransactionRequest) (*ledgerv1.Transaction, error) {
	view, err := s.ledger.TransactionByID(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	return transactionToProto(view), nil
}

// ReverseTransaction implements ledgerv1.LedgerServiceServer.
func (s *Server) ReverseTransaction(ctx context.Context, req *ledgerv1.ReverseTransactionRequest) (*ledgerv1.Transaction, error) {
	idem, err := idempotentRequest(ctx, "grpc:ReverseTransaction", req)
	if err != nil {
		return nil, err
	}

	outcome, err := s.ledger.ReverseTransaction(ctx, idem, req.GetTransactionId(), app.ReverseTransactionCommand{
		Reason: req.GetReason(),
	})
	if err != nil {
		return nil, err
	}
	return transactionToProto(outcome.Value), nil
}
