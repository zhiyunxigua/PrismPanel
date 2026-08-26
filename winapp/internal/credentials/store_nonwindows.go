//go:build !windows

package credentials

import "time"

type unsupportedStore struct{}

func NewStore() Store { return unsupportedStore{} }

func (unsupportedStore) List(string) ([]Account, error)         { return nil, ErrUnsupported }
func (unsupportedStore) Get(string, string) (Credential, error) { return Credential{}, ErrUnsupported }
func (unsupportedStore) Save(string, string, string, time.Time) (Account, error) {
	return Account{}, ErrUnsupported
}
func (unsupportedStore) Delete(string, string) error              { return ErrUnsupported }
func (unsupportedStore) ClearAll(string) error                    { return ErrUnsupported }
func (unsupportedStore) AutoLoginAccount(string) (string, error)  { return "", ErrUnsupported }
func (unsupportedStore) SetAutoLoginAccount(string, string) error { return ErrUnsupported }
