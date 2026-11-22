package publicweb

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
)

const DefaultUsername = "admin"
const accountDirectory = ".public-account"

var (
	ErrAlreadyInitialized = errors.New("public Web: the administrator account already exists; use the local reset-password command if needed")
	ErrAccountRecovery    = errors.New("public Web: administrator account metadata needs local recovery; do not remove its initialization marker")
	ErrInvalidUsername    = errors.New("public Web: username must be 1-64 characters: letters, numbers, period, underscore or hyphen")
	accountUsername       = regexp.MustCompile("^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$")
)

type Account struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

type accountFile struct {
	Version int `json:"version"`
	Account
	Generation string `json:"generation"`
}

type AccountStatus struct {
	State   string   `json:"state"`
	Account *Account `json:"account,omitempty"`
}

func accountPath(data string) string { return filepath.Join(data, accountDirectory, "account.json") }

// The directory also marks a partially created or damaged account. Missing
// metadata in a marked instance must never be treated as an old installation.
func readAccount(data string) (accountFile, error) {
	dir := filepath.Join(data, accountDirectory)
	info, err := os.Lstat(dir)
	if err != nil {
		return accountFile{}, err
	}
	if !info.IsDir() || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return accountFile{}, ErrAccountRecovery
	}
	raw, err := readProtected(accountPath(data))
	if err != nil {
		return accountFile{}, ErrAccountRecovery
	}
	var a accountFile
	if json.Unmarshal(raw, &a) != nil || a.Version != 1 || !validGeneration(a.ID) || !validGeneration(a.Generation) || !accountUsername.MatchString(a.Username) {
		return accountFile{}, ErrAccountRecovery
	}
	return a, nil
}

func createAccount(data, username, generation string) (accountFile, error) {
	if err := os.Mkdir(filepath.Join(data, accountDirectory), 0700); err != nil {
		return accountFile{}, err
	}
	a := accountFile{Version: 1, Account: Account{ID: randomToken(), Username: username}, Generation: generation}
	if err := writeProtected(accountPath(data), a); err != nil {
		return accountFile{}, err
	}
	return a, nil
}

// loadAccount is called with credentialFiles held. Only the Owner (or an
// offline writer holding its store lock) may establish or reconcile metadata.
func loadAccount(data string, reconcile bool) (accountFile, credential, error) {
	c, err := loadCredential(data)
	if err != nil {
		return accountFile{}, credential{}, ErrCredential
	}
	a, err := readAccount(data)
	switch {
	case reconcile && errors.Is(err, os.ErrNotExist):
		a, err = createAccount(data, DefaultUsername, c.Generation)
	case reconcile && err == nil && a.Generation != c.Generation:
		// An old binary may have replaced the password. The v1 credential
		// remains authoritative; preserve ID/name, never restore an old hash.
		a.Generation = c.Generation
		err = writeProtected(accountPath(data), a)
	}
	if err != nil || a.Generation != c.Generation {
		return accountFile{}, credential{}, errors.Join(ErrAccountRecovery, err)
	}
	return a, c, nil
}

func InspectAccount(data string) AccountStatus {
	credentialFiles.Lock()
	defer credentialFiles.Unlock()
	a, aerr := readAccount(data)
	c, cerr := loadCredential(data)
	switch {
	case errors.Is(aerr, os.ErrNotExist) && errors.Is(cerr, os.ErrNotExist):
		return AccountStatus{State: "uninitialized"}
	case errors.Is(aerr, os.ErrNotExist) && cerr == nil:
		return AccountStatus{State: "legacy"}
	case aerr == nil && cerr == nil && a.Generation == c.Generation:
		return AccountStatus{State: "ready", Account: &a.Account}
	case aerr == nil && cerr == nil:
		return AccountStatus{State: "password_changed", Account: &a.Account}
	default:
		return AccountStatus{State: "recovery_required"}
	}
}

func CredentialReady(data string) bool {
	state := InspectAccount(data).State
	return state == "ready" || state == "legacy" || state == "password_changed"
}

func InitAccount(data, username string, password []byte) (Account, error) {
	return writeAccountPassword(data, username, password, true, true)
}

func ResetPassword(data string, password []byte) error {
	_, err := writeAccountPassword(data, DefaultUsername, password, false, false)
	return err
}

// SetPassword is the compatibility entry for the trusted local public password
// command. Like administrator init, it may establish the first account.
func SetPassword(data string, password []byte) error {
	_, err := writeAccountPassword(data, DefaultUsername, password, false, true)
	return err
}

func writeAccountPassword(data, username string, password []byte, initOnly, allowCreate bool) (Account, error) {
	if !accountUsername.MatchString(username) {
		return Account{}, ErrInvalidUsername
	}
	c, err := makeCredential(password)
	if err != nil {
		return Account{}, err
	}
	defer clear(c.Hash)
	credentialFiles.Lock()
	defer credentialFiles.Unlock()
	a, aerr := readAccount(data)
	_, cerr := loadCredential(data)
	if initOnly && (aerr == nil || cerr == nil) {
		return Account{}, ErrAlreadyInitialized
	}
	if aerr != nil && !errors.Is(aerr, os.ErrNotExist) || initOnly && !errors.Is(cerr, os.ErrNotExist) {
		return Account{}, ErrAccountRecovery
	}
	if errors.Is(aerr, os.ErrNotExist) {
		if !allowCreate && errors.Is(cerr, os.ErrNotExist) {
			return Account{}, ErrCredential
		}
		a, err = createAccount(data, username, c.Generation)
		if err != nil {
			return Account{}, err
		}
	}
	if err = writeProtected(filepath.Join(data, credentialName), c); err != nil {
		return Account{}, err
	}
	if a.Generation != c.Generation {
		a.Generation = c.Generation
		if err = writeProtected(accountPath(data), a); err != nil {
			return Account{}, err
		}
	}
	return a.Account, nil
}

func renameAccount(data, username, generation string) (Account, error) {
	if !accountUsername.MatchString(username) {
		return Account{}, ErrInvalidUsername
	}
	credentialFiles.Lock()
	defer credentialFiles.Unlock()
	a, _, err := loadAccount(data, false)
	if err != nil || a.Generation != generation {
		return Account{}, ErrAccountRecovery
	}
	a.Username = username
	if err := writeProtected(accountPath(data), a); err != nil {
		return Account{}, err
	}
	return a.Account, nil
}
