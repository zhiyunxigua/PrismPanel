package game

import "errors"

var ErrMCNone = errors.New("no local Minecraft account")

type MCLocalStore interface {
	Load() (MCAccount, error)
	Save(MCAccount) error
	Delete() error
}

func NewMCLocalStore() MCLocalStore { return newMCLocalStore() }
