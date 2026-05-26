package auth

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	log "github.com/sirupsen/logrus"
	"golang.org/x/crypto/bcrypt"
)

const AccountFile = "/etc/kvm/pwd"

type Account struct {
	Username string `json:"username"`
	Password string `json:"password"` // should be named HashedPassword for clarity
}

type accountSnapshot struct {
	exists  bool
	content []byte
	mode    os.FileMode
}

func GetAccount() (*Account, error) {
	if _, err := os.Stat(AccountFile); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return getDefaultAccount(), nil
		}
		return nil, err
	}

	content, err := os.ReadFile(AccountFile)
	if err != nil {
		return nil, err
	}

	var account Account
	if err = json.Unmarshal(content, &account); err != nil {
		log.Errorf("unmarshal account failed: %s", err)
		return nil, err
	}

	return &account, nil
}

func SetAccount(username string, hashedPassword string) error {
	account, err := json.Marshal(&Account{
		Username: username,
		Password: hashedPassword,
	})
	if err != nil {
		log.Errorf("failed to marshal account information to json: %s", err)
		return err
	}

	err = os.MkdirAll(filepath.Dir(AccountFile), 0o755)
	if err != nil {
		log.Errorf("create directory %s failed: %s", AccountFile, err)
		return err
	}

	err = os.WriteFile(AccountFile, account, 0o644)
	if err != nil {
		log.Errorf("write password failed: %s", err)
		return err
	}

	return nil
}

func CompareAccount(username string, password string) bool {
	account, err := GetAccount()
	if err != nil {
		return false
	}

	if username != account.Username {
		return false
	}

	err = bcrypt.CompareHashAndPassword([]byte(account.Password), []byte(password))
	return err == nil
}

func backupAccount() (*accountSnapshot, error) {
	info, err := os.Stat(AccountFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &accountSnapshot{}, nil
		}
		return nil, err
	}

	content, err := os.ReadFile(AccountFile)
	if err != nil {
		return nil, err
	}

	return &accountSnapshot{
		exists:  true,
		content: content,
		mode:    info.Mode().Perm(),
	}, nil
}

func restoreAccount(snapshot *accountSnapshot) error {
	if snapshot == nil {
		return nil
	}

	if !snapshot.exists {
		if err := os.Remove(AccountFile); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(AccountFile), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(AccountFile, snapshot.content, snapshot.mode); err != nil {
		return err
	}

	return os.Chmod(AccountFile, snapshot.mode)
}

func DelAccount() error {
	if err := os.Remove(AccountFile); err != nil {
		log.Errorf("failed to delete password: %s", err)
		return err
	}

	return nil
}

func getDefaultAccount() *Account {
	hashedPassword, _ := bcrypt.GenerateFromPassword([]byte("admin"), bcrypt.DefaultCost)

	return &Account{
		Username: "admin",
		Password: string(hashedPassword),
	}
}
