package grpcapi

import (
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ViseonDev/ledger-core/internal/app"
	"github.com/ViseonDev/ledger-core/internal/domain/ledger"
	ledgerv1 "github.com/ViseonDev/ledger-core/proto/ledger/v1"
)

// accountTypeToProto maps the domain's account type onto the wire enum.
func accountTypeToProto(name string) ledgerv1.AccountType {
	switch name {
	case ledger.Asset.String():
		return ledgerv1.AccountType_ACCOUNT_TYPE_ASSET
	case ledger.Liability.String():
		return ledgerv1.AccountType_ACCOUNT_TYPE_LIABILITY
	case ledger.Equity.String():
		return ledgerv1.AccountType_ACCOUNT_TYPE_EQUITY
	case ledger.Revenue.String():
		return ledgerv1.AccountType_ACCOUNT_TYPE_REVENUE
	case ledger.Expense.String():
		return ledgerv1.AccountType_ACCOUNT_TYPE_EXPENSE
	default:
		return ledgerv1.AccountType_ACCOUNT_TYPE_UNSPECIFIED
	}
}

// accountTypeFromProto maps the wire enum onto the domain's string form.
func accountTypeFromProto(t ledgerv1.AccountType) string {
	switch t {
	case ledgerv1.AccountType_ACCOUNT_TYPE_ASSET:
		return ledger.Asset.String()
	case ledgerv1.AccountType_ACCOUNT_TYPE_LIABILITY:
		return ledger.Liability.String()
	case ledgerv1.AccountType_ACCOUNT_TYPE_EQUITY:
		return ledger.Equity.String()
	case ledgerv1.AccountType_ACCOUNT_TYPE_REVENUE:
		return ledger.Revenue.String()
	case ledgerv1.AccountType_ACCOUNT_TYPE_EXPENSE:
		return ledger.Expense.String()
	default:
		return ""
	}
}

func accountStatusToProto(name string) ledgerv1.AccountStatus {
	switch name {
	case ledger.Active.String():
		return ledgerv1.AccountStatus_ACCOUNT_STATUS_ACTIVE
	case ledger.Closed.String():
		return ledgerv1.AccountStatus_ACCOUNT_STATUS_CLOSED
	default:
		return ledgerv1.AccountStatus_ACCOUNT_STATUS_UNSPECIFIED
	}
}

func directionToProto(name string) ledgerv1.Direction {
	switch name {
	case ledger.Debit.String():
		return ledgerv1.Direction_DIRECTION_DEBIT
	case ledger.Credit.String():
		return ledgerv1.Direction_DIRECTION_CREDIT
	default:
		return ledgerv1.Direction_DIRECTION_UNSPECIFIED
	}
}

func directionFromProto(d ledgerv1.Direction) string {
	switch d {
	case ledgerv1.Direction_DIRECTION_DEBIT:
		return ledger.Debit.String()
	case ledgerv1.Direction_DIRECTION_CREDIT:
		return ledger.Credit.String()
	default:
		return ""
	}
}

func accountToProto(view app.AccountView) *ledgerv1.Account {
	return &ledgerv1.Account{
		Id:            view.ID,
		Code:          view.Code,
		Name:          view.Name,
		Type:          accountTypeToProto(view.Type),
		Currency:      view.Currency,
		AllowNegative: view.AllowNegative,
		Status:        accountStatusToProto(view.Status),
	}
}

func transactionToProto(view app.TransactionView) *ledgerv1.Transaction {
	entries := make([]*ledgerv1.Entry, 0, len(view.Entries))
	for _, e := range view.Entries {
		entries = append(entries, &ledgerv1.Entry{
			AccountId:   e.AccountID,
			AccountCode: e.AccountCode,
			Direction:   directionToProto(e.Direction),
			Amount:      e.Amount,
		})
	}

	return &ledgerv1.Transaction{
		Id:         view.ID,
		Reference:  view.Reference,
		Currency:   view.Currency,
		PostedAt:   timestamppb.New(view.PostedAt),
		ReversalOf: view.ReversalOf,
		Metadata:   view.Metadata,
		Entries:    entries,
	}
}

func balanceToProto(view app.BalanceView) *ledgerv1.Balance {
	return &ledgerv1.Balance{
		AccountId:     view.AccountID,
		Currency:      view.Currency,
		Debits:        view.Debits,
		Credits:       view.Credits,
		Balance:       view.Balance,
		Authoritative: view.Authoritative,
	}
}

func entryInputsFromProto(inputs []*ledgerv1.EntryInput) []app.EntryInput {
	out := make([]app.EntryInput, 0, len(inputs))
	for _, in := range inputs {
		out = append(out, app.EntryInput{
			AccountCode: in.GetAccountCode(),
			Direction:   directionFromProto(in.GetDirection()),
			Amount:      in.GetAmount(),
		})
	}
	return out
}
