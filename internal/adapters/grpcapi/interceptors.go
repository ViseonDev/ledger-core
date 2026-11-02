package grpcapi

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/ViseonDev/ledger-core/internal/domain/ledger"
	"github.com/ViseonDev/ledger-core/internal/domain/money"
	"github.com/ViseonDev/ledger-core/internal/ports"
)

// idempotencyKeyMetadata is the metadata key carrying the idempotency key.
const idempotencyKeyMetadata = "idempotency-key"

// idempotencyKeyFrom extracts the key from request metadata.
func idempotencyKeyFrom(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", ports.ErrIdempotencyKeyRequired
	}
	values := md.Get(idempotencyKeyMetadata)
	if len(values) == 0 || values[0] == "" {
		return "", ports.ErrIdempotencyKeyRequired
	}
	return values[0], nil
}

// codeFor maps a domain error to a gRPC status code.
func codeFor(err error) codes.Code {
	switch {
	case errors.Is(err, ledger.ErrUnbalanced),
		errors.Is(err, ledger.ErrInsufficientFunds),
		errors.Is(err, ledger.ErrAccountClosed):
		return codes.FailedPrecondition

	case errors.Is(err, money.ErrCurrencyMismatch),
		errors.Is(err, money.ErrInvalidCurrency),
		errors.Is(err, ledger.ErrInvalidDirection),
		errors.Is(err, ledger.ErrInvalidAccountType),
		errors.Is(err, ledger.ErrInvalidAccountCode),
		errors.Is(err, ledger.ErrInvalidAccountName),
		errors.Is(err, ledger.ErrAccountCodeTypeMismatch),
		errors.Is(err, ledger.ErrInvalidID),
		errors.Is(err, ledger.ErrTooFewEntries),
		errors.Is(err, ledger.ErrNonPositiveAmount),
		errors.Is(err, ledger.ErrInvalidMetadata),
		errors.Is(err, ledger.ErrReversalReasonRequired),
		errors.Is(err, ledger.ErrInvalidPostedAt):
		return codes.InvalidArgument

	case errors.Is(err, ledger.ErrAccountNotFound),
		errors.Is(err, ledger.ErrTransactionNotFound):
		return codes.NotFound

	case errors.Is(err, ports.ErrIdempotencyConflict),
		errors.Is(err, ledger.ErrAlreadyReversed):
		return codes.Aborted

	case errors.Is(err, ledger.ErrAccountExists),
		errors.Is(err, ledger.ErrTransactionExists):
		return codes.AlreadyExists

	case errors.Is(err, ports.ErrIdempotencyKeyRequired),
		errors.Is(err, ports.ErrIdempotencyKeyTooLong):
		return codes.InvalidArgument

	case errors.Is(err, context.Canceled):
		return codes.Canceled

	case errors.Is(err, context.DeadlineExceeded):
		return codes.DeadlineExceeded

	default:
		return codes.Internal
	}
}

// errorInterceptor converts domain errors into gRPC statuses.
func errorInterceptor(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		response, err := handler(ctx, req)
		if err == nil {
			return response, nil
		}

		// Already a gRPC status: pass it through.
		if _, ok := status.FromError(err); ok && status.Code(err) != codes.Unknown {
			return nil, err
		}

		code := codeFor(err)
		if code == codes.Internal {
			logger.ErrorContext(ctx, "rpc failed",
				slog.String("method", info.FullMethod),
				slog.String("error", err.Error()),
			)
			// Don't leak SQL or account codes to the client.
			return nil, status.Error(codes.Internal, "internal error")
		}

		logger.InfoContext(ctx, "rpc rejected",
			slog.String("method", info.FullMethod),
			slog.String("code", code.String()),
			slog.String("error", err.Error()),
		)
		return nil, status.Error(code, err.Error())
	}
}

// loggingInterceptor emits one line per RPC.
func loggingInterceptor(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		started := time.Now()

		response, err := handler(ctx, req)

		logger.LogAttrs(ctx, slog.LevelInfo, "grpc request",
			slog.String("method", info.FullMethod),
			slog.String("code", status.Code(err).String()),
			slog.Duration("duration", time.Since(started)),
		)
		return response, err
	}
}
