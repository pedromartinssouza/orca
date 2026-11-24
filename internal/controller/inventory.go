/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	cachev1alpha1 "github.com/pedromartinssouza/orca/api/v1alpha1"
)

const minDappID, maxDappID int32 = 1, 100

// allocateDappID returns the existing ID for ref, or the lowest free ID on the node.
func allocateDappID(entries []cachev1alpha1.DappInventoryEntry, ref cachev1alpha1.DappRef) (int32, error) {
	if i := findEntry(entries, ref); i >= 0 {
		return entries[i].DappID, nil
	}
	used := map[int32]bool{}
	for _, e := range entries {
		used[e.DappID] = true
	}
	for id := minDappID; id <= maxDappID; id++ {
		if !used[id] {
			return id, nil
		}
	}
	return 0, fmt.Errorf("no free dApp ID in %d..%d", minDappID, maxDappID)
}

// newToken returns a random E3 token and its SHA-256 hex hash.
func newToken() (token, hash string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	token = hex.EncodeToString(b)
	return token, hashToken(token), nil
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func findEntry(entries []cachev1alpha1.DappInventoryEntry, ref cachev1alpha1.DappRef) int {
	for i, e := range entries {
		if e.Dapp == ref {
			return i
		}
	}
	return -1
}

func findEntryStatus(status []cachev1alpha1.DappInventoryEntryStatus, id int32) *cachev1alpha1.DappInventoryEntryStatus {
	for i := range status {
		if status[i].DappID == id {
			return &status[i]
		}
	}
	return nil
}
