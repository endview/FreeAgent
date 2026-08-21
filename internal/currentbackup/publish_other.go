//go:build !windows && !linux && !darwin

package currentbackup

import "fmt"

func publishNoReplace(_, _ string) error {
	return fmt.Errorf("%w: no atomic no-replace publication primitive on this OS", ErrInvalidInput)
}
