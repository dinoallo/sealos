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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStateStoreRoundTripAndTransaction(t *testing.T) {
	store, err := NewStateStore(t.TempDir(), "target-a")
	require.NoError(t, err)
	state := &State{
		Distribution:        "cloud@v1.0.0",
		ManifestFingerprint: "sha256:manifest",
		Packages: []InstalledPackage{{
			Name: "demo", Version: "v1.0.0", Fingerprint: "sha256:package", Status: PackageStatusInstalled,
		}},
	}
	require.NoError(t, store.Save(state))
	loaded, err := store.Load()
	require.NoError(t, err)
	require.Equal(t, PackageStateSchema, loaded.SchemaVersion)
	require.Equal(t, "target-a", loaded.TargetID)
	require.Equal(t, state.Distribution, loaded.Distribution)
	require.Equal(t, state.Packages, loaded.Packages)

	transaction := &Transaction{ID: "tx-1", Distribution: "cloud@v1.1.0", Status: TransactionApplying}
	require.NoError(t, store.SaveTransaction(transaction))
	data, err := os.ReadFile(filepath.Join(store.Root, "targets", store.TargetID, "transactions", "tx-1.json"))
	require.NoError(t, err)
	require.Contains(t, string(data), `"status": "applying"`)
}

func TestStateStoreMissingState(t *testing.T) {
	store, err := NewStateStore(t.TempDir(), "target-a")
	require.NoError(t, err)
	_, err = store.Load()
	require.ErrorIs(t, err, ErrPackageStateNotFound)
}

func TestStateStoreRejectsDuplicatePackages(t *testing.T) {
	store, err := NewStateStore(t.TempDir(), "target-a")
	require.NoError(t, err)
	err = store.Save(&State{Packages: []InstalledPackage{
		{Name: "demo", Version: "v1", Fingerprint: "sha256:a"},
		{Name: "demo", Version: "v2", Fingerprint: "sha256:b"},
	}})
	require.ErrorContains(t, err, `duplicate package "demo"`)
}

func TestTargetIDIsStableForMasterOrder(t *testing.T) {
	left := TargetID("192.0.2.2:22,192.0.2.1:22", 22)
	right := TargetID("192.0.2.1:22,192.0.2.2:22", 22)
	require.Equal(t, left, right)
	require.Len(t, left, 64)
}

func TestStateStoreLockHonorsContext(t *testing.T) {
	store, err := NewStateStore(t.TempDir(), "target-a")
	require.NoError(t, err)
	unlock, err := store.Lock(context.Background())
	require.NoError(t, err)
	defer unlock()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = store.Lock(ctx)
	require.ErrorIs(t, err, context.Canceled)
}

func TestStateStoreRejectsInvalidTargetID(t *testing.T) {
	_, err := NewStateStore(t.TempDir(), "../target")
	require.Error(t, err)
	_, err = NewStateStore(t.TempDir(), "")
	require.Error(t, err)
}
