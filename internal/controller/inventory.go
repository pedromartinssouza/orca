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
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

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

const (
	// ManagedByLabel marks the token Secrets so that the manager only caches ORCA's own Secrets.
	ManagedByLabel = "app.kubernetes.io/managed-by"
	// dappUIDLabel ties a token Secret to its DappManifest, so stale ones can be pruned.
	dappUIDLabel = "orca.cache.orca.com/dapp-uid"
	// dappGenerationAnnotation is the DappManifest generation that triggered the rotation.
	dappGenerationAnnotation = "orca.cache.orca.com/dapp-generation"
)

// tokenSecretName is the Secret, in dm.Spec.Namespace, that holds the token whose hash is
// tokenHash, under key "token". Each rotation gets a new name, so the HelmRelease values
// change and Flux rolls the dApp pod. The name stays a DNS-1123 subdomain of at most 253 chars.
func tokenSecretName(dm *cachev1alpha1.DappManifest, tokenHash string) string {
	suffix := "-e3-" + tokenHash[:min(10, len(tokenHash))]
	prefix := dm.Namespace + "-" + dm.Name
	if limit := 253 - len(suffix); len(prefix) > limit {
		prefix = strings.TrimRight(prefix[:limit], "-.")
	}
	return prefix + suffix
}

func (r *DappManifestReconciler) getOrCreateInventory(ctx context.Context, node string) (*cachev1alpha1.DappInventory, error) {
	inv := &cachev1alpha1.DappInventory{}
	err := r.Get(ctx, types.NamespacedName{Name: node}, inv)
	if apierrors.IsNotFound(err) {
		inv = &cachev1alpha1.DappInventory{ObjectMeta: metav1.ObjectMeta{Name: node}}
		err = r.Create(ctx, inv)
	}
	return inv, err
}

// reconcileInventory reserves the dApp ID and rotates the E3 token when the entry is new or
// its current Secret is missing, stale (other DappManifest generation) or does not match the
// stored hash. The inventory is written before the Secret, so a failed Secret write is seen
// as a missing Secret on the next reconcile.
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

	i := findEntry(inv.Spec.Entries, ref)
	if i >= 0 {
		current, err := r.tokenSecretCurrent(ctx, dm, inv.Spec.Entries[i])
		if err != nil || current {
			return inv, inv.Spec.Entries[i], err
		}
	}

	token, hash, err := newToken()
	if err != nil {
		return nil, entry, err
	}
	entry = cachev1alpha1.DappInventoryEntry{Dapp: ref, DappID: id, TokenHash: hash, Generation: 1}
	if i >= 0 {
		entry.Generation = inv.Spec.Entries[i].Generation + 1
		inv.Spec.Entries[i] = entry
	} else {
		// A reused ID must not match a stale Gateway status left by its previous holder.
		for _, st := range inv.Status.Entries {
			if st.DappID == id && st.Generation >= entry.Generation {
				entry.Generation = st.Generation + 1
			}
		}
		inv.Spec.Entries = append(inv.Spec.Entries, entry)
	}
	if err := r.Update(ctx, inv); err != nil {
		return nil, entry, err
	}
	if err := r.createTokenSecret(ctx, dm, token, hash); err != nil {
		return nil, entry, err
	}
	return inv, entry, nil
}

// tokenSecretCurrent reports whether the entry's Secret exists, was written for the current
// DappManifest generation, and holds the token the entry hashes. It reads uncached.
func (r *DappManifestReconciler) tokenSecretCurrent(ctx context.Context, dm *cachev1alpha1.DappManifest, e cachev1alpha1.DappInventoryEntry) (bool, error) {
	secret := &corev1.Secret{}
	key := types.NamespacedName{Name: tokenSecretName(dm, e.TokenHash), Namespace: dm.Spec.Namespace}
	if err := r.APIReader.Get(ctx, key, secret); err != nil {
		return false, client.IgnoreNotFound(err)
	}
	return secret.Annotations[dappGenerationAnnotation] == strconv.FormatInt(dm.Generation, 10) &&
		hashToken(string(secret.Data["token"])) == e.TokenHash, nil
}

// createTokenSecret makes sure the target namespace exists and creates the Secret for a new token.
func (r *DappManifestReconciler) createTokenSecret(ctx context.Context, dm *cachev1alpha1.DappManifest, token, hash string) error {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: dm.Spec.Namespace}}
	if err := r.Create(ctx, ns); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        tokenSecretName(dm, hash),
			Namespace:   dm.Spec.Namespace,
			Labels:      map[string]string{ManagedByLabel: "orca", dappUIDLabel: string(dm.UID)},
			Annotations: map[string]string{dappGenerationAnnotation: strconv.FormatInt(dm.Generation, 10)},
		},
		Data: map[string][]byte{"token": []byte(token)},
	}
	err := r.Create(ctx, secret)
	if !apierrors.IsAlreadyExists(err) {
		return err
	}
	existing := &corev1.Secret{}
	if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(secret), existing); err != nil {
		return err
	}
	if hashToken(string(existing.Data["token"])) != hash {
		return fmt.Errorf("token Secret %s/%s exists with another token", secret.Namespace, secret.Name)
	}
	return nil
}

// pruneTokenSecrets deletes the dApp's token Secrets other than current. It reads uncached.
func (r *DappManifestReconciler) pruneTokenSecrets(ctx context.Context, dm *cachev1alpha1.DappManifest, current string) error {
	list := &corev1.SecretList{}
	if err := r.APIReader.List(ctx, list, client.InNamespace(dm.Spec.Namespace),
		client.MatchingLabels{dappUIDLabel: string(dm.UID)}); err != nil {
		return err
	}
	for i := range list.Items {
		if list.Items[i].Name == current {
			continue
		}
		if err := r.Delete(ctx, &list.Items[i]); client.IgnoreNotFound(err) != nil {
			return err
		}
	}
	return nil
}
