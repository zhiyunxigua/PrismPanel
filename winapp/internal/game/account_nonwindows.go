//go:build !windows

package game

import "errors"

type localAccountStore struct{}

func newLocalAccountStore() LocalAccountStore { return localAccountStore{} }

func (localAccountStore) Load() (AccountState, error) {
	return AccountState{}, errors.New("local NetEase account storage is unavailable on this platform")
}
func (localAccountStore) Save(AccountState) error {
	return errors.New("local NetEase account storage is unavailable on this platform")
}
func (localAccountStore) Delete() error {
	return errors.New("local NetEase account storage is unavailable on this platform")
}
