// Package seed generates deterministic synthetic data for demonstrations.
package seed

import (
	"fmt"

	"github.com/google/uuid"

	"github.com/ViseonDev/ledger-core/internal/domain/ledger"
)

// namespace is a fixed UUID that scopes every identifier this package derives.
var namespace = uuid.MustParse("6f1f1b64-3f2e-4a5e-9d2c-8e7a1b0c3d40")

// deterministicUUID derives a stable v5 UUID from a name.
func deterministicUUID(name string) uuid.UUID {
	return uuid.NewSHA1(namespace, []byte(name))
}

func accountID(code string) (ledger.AccountID, error) {
	return ledger.ParseAccountID(deterministicUUID("account:" + code).String())
}

func transactionID(name string) (ledger.TransactionID, error) {
	return ledger.ParseTransactionID(deterministicUUID("transaction:" + name).String())
}

func customerRef(n int) string { return fmt.Sprintf("cus_demo_%04d", n) }
func merchantRef(n int) string { return fmt.Sprintf("mer_demo_%02d", n) }
