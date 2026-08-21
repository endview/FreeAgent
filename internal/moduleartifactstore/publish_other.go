//go:build !windows && !linux && !darwin

package moduleartifactstore

import "errors"

func publishNoReplaceV1(string, string) error {
	return errors.New("module artifact store: atomic no-replace publication is unsupported")
}
