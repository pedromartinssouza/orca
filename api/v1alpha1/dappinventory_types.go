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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DappRef identifies the DappManifest that owns an inventory entry.
type DappRef struct {
	// Namespace of the DappManifest.
	Namespace string `json:"namespace"`
	// Name of the DappManifest.
	Name string `json:"name"`
}

// DappInventoryEntry is one dApp admitted on the node. ORCA writes it.
type DappInventoryEntry struct {
	// Dapp is the DappManifest this entry belongs to.
	Dapp DappRef `json:"dapp"`
	// DappID is the E3 dApp ID reserved for the dApp on this node, unique per node.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	DappID int32 `json:"dappId"`
	// TokenHash is the SHA-256 hex hash of the dApp's current E3 token. The token itself
	// is only in a Secret mounted into the dApp pod.
	// +kubebuilder:validation:Pattern=`^[0-9a-f]{64}$`
	TokenHash string `json:"tokenHash"`
	// Generation is the rotation counter: it starts at 1 and is bumped on every token rotation.
	// The Gateway reports Registered and Advertised for this generation.
	Generation int64 `json:"generation"`
}

// DappInventorySpec is the desired per-node dApp inventory, written by ORCA.
type DappInventorySpec struct {
	// Entries are the dApps admitted on the node. A dApp that leaves the spec is to be
	// withdrawn by the Gateway.
	// +listType=map
	// +listMapKey=dappId
	Entries []DappInventoryEntry `json:"entries,omitempty"`
}

// DappInventoryEntryStatus is the Gateway's observed state of one dApp ID.
type DappInventoryEntryStatus struct {
	// DappID is the dApp ID this status entry reports on.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	DappID int32 `json:"dappId"`
	// Generation is the spec entry generation the Gateway observed.
	Generation int64 `json:"generation"`
	// Registered is true once the Gateway accepted the dApp's E3 Setup with the reserved ID.
	Registered bool `json:"registered"`
	// Advertised is true once the RIC acknowledged the RIC Service Update that adds the dApp.
	Advertised bool `json:"advertised"`
}

// DappInventoryStatus is the observed per-node inventory, written by the Gateway.
type DappInventoryStatus struct {
	// Entries hold one entry per dApp ID the Gateway knows of. The Gateway writes an entry
	// before it accepts an E3 Setup for the ID, and removes it only after it withdrew the
	// dApp from the RIC, which confirms the withdrawal to ORCA.
	// +listType=map
	// +listMapKey=dappId
	Entries []DappInventoryEntryStatus `json:"entries,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// DappInventory is the per-node dApp inventory, named after the node.
type DappInventory struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DappInventorySpec   `json:"spec,omitempty"`
	Status DappInventoryStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DappInventoryList contains a list of DappInventory.
type DappInventoryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DappInventory `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DappInventory{}, &DappInventoryList{})
}
