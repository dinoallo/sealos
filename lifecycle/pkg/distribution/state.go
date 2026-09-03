// Copyright 2026 sealos.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package distribution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	DefaultPackageStateDir = "/var/lib/sealos/package-state"
	PackageStateSchema     = 1

	PackageStatusInstalled = "installed"
	PackageStatusAdopted   = "adopted"

	TransactionPending   = "pending"
	TransactionApplying  = "applying"
	TransactionSucceeded = "succeeded"
	TransactionFailed    = "failed"
)

var ErrPackageStateNotFound = errors.New("package state not found")

// InstalledPackage describes the package artifact recorded by the package
// manager after a successful installation or explicit adoption.
type InstalledPackage struct {
	Name        string      `json:"name"`
	Version     string      `json:"version"`
	Fingerprint string      `json:"fingerprint"`
	Image       string      `json:"image,omitempty"`
	Mode        ResolveMode `json:"mode,omitempty"`
	Status      string      `json:"status"`
	InstalledAt time.Time   `json:"installedAt"`
}

func (p InstalledPackage) Ref() string {
	return p.Name + "@" + p.Version
}

func NewInstalledPackage(pkg Package, image string, mode ResolveMode, status string) InstalledPackage {
	if status == "" {
		status = PackageStatusInstalled
	}
	return InstalledPackage{
		Name:        pkg.Name,
		Version:     pkg.Version,
		Fingerprint: pkg.Fingerprint(),
		Image:       image,
		Mode:        mode,
		Status:      status,
		InstalledAt: time.Now().UTC(),
	}
}

// State is the package database for one target environment.
type State struct {
	SchemaVersion       int                `json:"schemaVersion"`
	TargetID            string             `json:"targetID"`
	Distribution        string             `json:"distribution"`
	ManifestFingerprint string             `json:"manifestFingerprint"`
	RepositoryCommit    string             `json:"repositoryCommit,omitempty"`
	Packages            []InstalledPackage `json:"packages"`
	UpdatedAt           time.Time          `json:"updatedAt"`
}

// Transaction records progress so a failed update can be retried without
// rerunning packages that already completed.
type Transaction struct {
	ID                  string               `json:"id"`
	TargetID            string               `json:"targetID"`
	Distribution        string               `json:"distribution"`
	ManifestFingerprint string               `json:"manifestFingerprint"`
	Status              string               `json:"status"`
	Packages            []TransactionPackage `json:"packages"`
	Error               string               `json:"error,omitempty"`
	StartedAt           time.Time            `json:"startedAt"`
	UpdatedAt           time.Time            `json:"updatedAt"`
}

type TransactionPackage struct {
	Ref    string `json:"ref"`
	Action string `json:"action"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type StateStore struct {
	Root     string
	TargetID string
}

func DefaultPackageStatePath() string {
	return DefaultPackageStateDir
}

func NewStateStore(root, targetID string) (*StateStore, error) {
	if strings.TrimSpace(root) == "" {
		root = DefaultPackageStateDir
	}
	if strings.TrimSpace(targetID) == "" {
		return nil, errors.New("target ID is required")
	}
	if filepath.Base(targetID) != targetID || targetID == "." || targetID == ".." {
		return nil, fmt.Errorf("invalid target ID %q", targetID)
	}
	return &StateStore{Root: root, TargetID: targetID}, nil
}

func (s *StateStore) targetDir() string {
	return filepath.Join(s.Root, "targets", s.TargetID)
}

func (s *StateStore) statePath() string {
	return filepath.Join(s.targetDir(), "state.json")
}

func (s *StateStore) transactionDir() string {
	return filepath.Join(s.targetDir(), "transactions")
}

func (s *StateStore) Load() (*State, error) {
	if s == nil {
		return nil, errors.New("state store is nil")
	}
	data, err := os.ReadFile(s.statePath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrPackageStateNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read package state: %w", err)
	}
	state := &State{}
	if err := json.Unmarshal(data, state); err != nil {
		return nil, fmt.Errorf("decode package state: %w", err)
	}
	if err := validateState(state, s.TargetID); err != nil {
		return nil, err
	}
	return state, nil
}

func (s *StateStore) Save(state *State) error {
	if s == nil {
		return errors.New("state store is nil")
	}
	if state == nil {
		return errors.New("package state is nil")
	}
	state.SchemaVersion = PackageStateSchema
	state.TargetID = s.TargetID
	state.UpdatedAt = time.Now().UTC()
	if err := validateState(state, s.TargetID); err != nil {
		return err
	}
	return writeJSONAtomically(s.statePath(), state)
}

func (s *StateStore) SaveTransaction(transaction *Transaction) error {
	if s == nil {
		return errors.New("state store is nil")
	}
	if transaction == nil {
		return errors.New("transaction is nil")
	}
	transaction.TargetID = s.TargetID
	transaction.UpdatedAt = time.Now().UTC()
	if transaction.StartedAt.IsZero() {
		transaction.StartedAt = transaction.UpdatedAt
	}
	if strings.TrimSpace(transaction.ID) == "" {
		return errors.New("transaction ID is required")
	}
	if filepath.Base(transaction.ID) != transaction.ID {
		return fmt.Errorf("invalid transaction ID %q", transaction.ID)
	}
	return writeJSONAtomically(filepath.Join(s.transactionDir(), transaction.ID+".json"), transaction)
}

func (s *StateStore) Lock(ctx context.Context) (func(), error) {
	if s == nil {
		return nil, errors.New("state store is nil")
	}
	if err := os.MkdirAll(s.targetDir(), 0o700); err != nil {
		return nil, fmt.Errorf("create package state directory: %w", err)
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	file, err := os.OpenFile(filepath.Join(s.targetDir(), "lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open package state lock: %w", err)
	}
	for {
		err = unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			var once sync.Once
			return func() {
				once.Do(func() {
					_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
					_ = file.Close()
				})
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			_ = file.Close()
			return nil, fmt.Errorf("lock package state: %w", err)
		}
		select {
		case <-ctx.Done():
			_ = file.Close()
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func validateState(state *State, targetID string) error {
	if state.SchemaVersion != 0 && state.SchemaVersion != PackageStateSchema {
		return fmt.Errorf("unsupported package state schema %d", state.SchemaVersion)
	}
	if state.TargetID != "" && state.TargetID != targetID {
		return fmt.Errorf("package state target ID %q does not match %q", state.TargetID, targetID)
	}
	seen := make(map[string]struct{}, len(state.Packages))
	for _, pkg := range state.Packages {
		if strings.TrimSpace(pkg.Name) == "" || strings.TrimSpace(pkg.Version) == "" {
			return errors.New("package state contains package without name or version")
		}
		if _, ok := seen[pkg.Name]; ok {
			return fmt.Errorf("package state contains duplicate package %q", pkg.Name)
		}
		seen[pkg.Name] = struct{}{}
	}
	return nil
}

func writeJSONAtomically(path string, value interface{}) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create package state directory: %w", err)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode package state: %w", err)
	}
	data = append(data, '\n')
	temporary, err := os.CreateTemp(filepath.Dir(path), ".state-*")
	if err != nil {
		return fmt.Errorf("create temporary package state: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set package state permissions: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write package state: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync package state: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close package state: %w", err)
	}
	if err := os.Rename(temporaryName, path); err != nil {
		return fmt.Errorf("replace package state: %w", err)
	}
	return nil
}

// TargetID returns a deterministic identifier without storing credentials.
func TargetID(masters string, sshPort uint16) string {
	items := strings.FieldsFunc(masters, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' })
	for index := range items {
		items[index] = strings.TrimSpace(items[index])
	}
	sort.Strings(items)
	return fingerprint(struct {
		Masters []string `json:"masters"`
		Port    uint16   `json:"port"`
	}{Masters: items, Port: sshPort})[len("sha256:"):]
}
