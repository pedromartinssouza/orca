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
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

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

// ManagedByLabel marks the token Secrets so that the manager only caches ORCA's own Secrets.
const ManagedByLabel = "app.kubernetes.io/managed-by"

// tokenSecretName is the Secret, in dm.Spec.Namespace, that holds the dApp's E3 token under key "token".
func tokenSecretName(dm *cachev1alpha1.DappManifest) string { return dm.Name + "-e3-token" }

func (r *DappManifestReconciler) getOrCreateInventory(ctx context.Context, node string) (*cachev1alpha1.DappInventory, error) {
	inv := &cachev1alpha1.DappInventory{}
	err := r.Get(ctx, types.NamespacedName{Name: node}, inv)
	if apierrors.IsNotFound(err) {
		inv = &cachev1alpha1.DappInventory{ObjectMeta: metav1.ObjectMeta{Name: node}}
		err = r.Create(ctx, inv)
	}
	return inv, err
}

// reconcileInventory reserves the dApp ID and rotates the E3 token when the entry is new,
// the spec generation changed, or the Secret no longer matches the stored hash.
// The inventory is written before the Secret, so a failed Secret write is caught as a
// hash mismatch on the next reconcile.
func (r *DappManifestReconciler) reconcileInventory(ctx context.Context, dm *cachev1alpha1.DappManifest) (*cachev1alpha1.DappInventory, cachev1alpha1.DappInventoryEntry, error) {
	var entry cachev1alpha1.DappInventoryEntry
	inv, err := r.getOrCreateInventory(ctx, dm.Spec.NodeName)
	if err != nil {
		return nil, entry, err
	}
	ref := cachev1alpha1.DappRef{Namespace: dm.Namespace, Name: dm.Name}
	id, err := allocateDappID(inv.Spec.Entries, ref)
	if err != nil {
		return nil, entry, err
	}

	secret := &corev1.Secret{}
	err = r.Get(ctx, types.NamespacedName{Name: tokenSecretName(dm), Namespace: dm.Spec.Namespace}, secret)
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, entry, err
	}
	secretFound := err == nil

	i := findEntry(inv.Spec.Entries, ref)
	if i >= 0 && secretFound && inv.Spec.Entries[i].Generation == dm.Generation &&
		hashToken(string(secret.Data["token"])) == inv.Spec.Entries[i].TokenHash {
		return inv, inv.Spec.Entries[i], nil
	}

	token, hash, err := newToken()
	if err != nil {
		return nil, entry, err
	}
	entry = cachev1alpha1.DappInventoryEntry{Dapp: ref, DappID: id, TokenHash: hash, Generation: dm.Generation}
	if i >= 0 {
		inv.Spec.Entries[i] = entry
	} else {
		inv.Spec.Entries = append(inv.Spec.Entries, entry)
	}
	if err := r.Update(ctx, inv); err != nil {
		return nil, entry, err
	}
	if err := r.reconcileTokenSecret(ctx, dm, token); err != nil {
		return nil, entry, err
	}
	return inv, entry, nil
}

// reconcileTokenSecret makes sure the target namespace exists and writes the token Secret.
func (r *DappManifestReconciler) reconcileTokenSecret(ctx context.Context, dm *cachev1alpha1.DappManifest, token string) error {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: dm.Spec.Namespace}}
	if err := r.Create(ctx, ns); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: tokenSecretName(dm), Namespace: dm.Spec.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		if secret.Labels == nil {
			secret.Labels = map[string]string{}
		}
		secret.Labels[ManagedByLabel] = "orca"
		secret.Data = map[string][]byte{"token": []byte(token)}
		return nil
	})
	return err
}
