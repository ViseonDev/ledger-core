package integration

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/ViseonDev/ledger-core/internal/adapters/grpcapi"
	"github.com/ViseonDev/ledger-core/internal/adapters/httpapi"
	"github.com/ViseonDev/ledger-core/internal/adapters/postgres"
	"github.com/ViseonDev/ledger-core/internal/app"
	"github.com/ViseonDev/ledger-core/internal/platform/observability"
	ledgerv1 "github.com/ViseonDev/ledger-core/proto/ledger/v1"
)

// newAPIOver builds an HTTP server over an existing application service.
func newAPIOver(t *testing.T, service *app.Ledger, store *postgres.Store) *httptest.Server {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := httptest.NewServer(httpapi.NewServer(httpapi.Config{
		Ledger:         service,
		Logger:         logger,
		Metrics:        observability.NewMetrics(),
		RequestTimeout: 10 * time.Second,
		Ready:          func() error { return store.Pool().Ping(t.Context()) },
	}).Routes())

	t.Cleanup(server.Close)
	return server
}

// newGRPCOver builds an in-memory gRPC client over an existing application
// service.
func newGRPCOver(t *testing.T, service *app.Ledger) ledgerv1.LedgerServiceClient {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	listener := bufconn.Listen(bufSize)
	server := grpcapi.Register(logger, service)

	go func() {
		if err := server.Serve(listener); err != nil {
			t.Logf("grpc serve stopped: %v", err)
		}
	}()

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)

	t.Cleanup(func() {
		_ = conn.Close()
		server.GracefulStop()
		_ = listener.Close()
	})

	return ledgerv1.NewLedgerServiceClient(conn)
}
