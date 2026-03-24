package ledger

import "errors"

var (
	ErrInsufficientFunds    = errors.New("insufficient funds")
	ErrAccountNotFound      = errors.New("account not found")
	ErrUserNotFound         = errors.New("user not found")
	ErrDuplicateTransaction = errors.New("duplicate transaction")
	ErrInvalidAmount        = errors.New("invalid amount")
	ErrSameAccount          = errors.New("source and destination accounts must be different")
	ErrCurrencyMismatch     = errors.New("currency mismatch")
	ErrContention           = errors.New("contention")
)
