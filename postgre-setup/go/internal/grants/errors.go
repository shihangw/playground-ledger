package grants

import "errors"

var (
	ErrInsufficientGrants = errors.New("insufficient grant balance")
	ErrGrantNotFound      = errors.New("grant not found")
	ErrGrantExpired       = errors.New("grant is expired")
	ErrInvalidAmount      = errors.New("invalid amount")
	ErrAccountNotFound    = errors.New("account not found")
	ErrDuplicateGrant     = errors.New("duplicate grant")
	ErrInsufficientFunds  = errors.New("insufficient funds")
)
