//go:build !windows

package capabilities

import "errors"

func protectSecret([]byte) ([]byte, error) {
	return nil, errors.New("managed capability secrets require the Windows credential protector")
}
func unprotectSecret([]byte) ([]byte, error) {
	return nil, errors.New("managed capability secrets require the Windows credential protector")
}
