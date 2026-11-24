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
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// DappInventoryEntry is one dApp admitted on the node.
type DappInventoryEntry struct {
	Dapp DappRef `json:"dapp"`
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	DappID     int32  `json:"dappId"`
	TokenHash  string `json:"tokenHash"`
	Generation int64  `json:"generation"`
}

// DappInventorySpec is the desired per-node dApp inventory.
type DappInventorySpec struct {
	// +listType=map
	// +listMapKey=dappId
	Entries []DappInventoryEntry `json:"entries,omitempty"`
}

// DappInventoryEntryStatus is the observed state of one entry.
type DappInventoryEntryStatus struct {
	DappID     int32 `json:"dappId"`
	Generation int64 `json:"generation"`
	Registered bool  `json:"registered"`
	Advertised bool  `json:"advertised"`
}

// DappInventoryStatus is the observed per-node inventory.
type DappInventoryStatus struct {
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
