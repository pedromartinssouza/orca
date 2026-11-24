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
	"testing"

	cachev1alpha1 "github.com/pedromartinssouza/orca/api/v1alpha1"
)

func ref(n string) cachev1alpha1.DappRef { return cachev1alpha1.DappRef{Namespace: "ns", Name: n} }

func TestAllocateDappIDReturnsExisting(t *testing.T) {
	es := []cachev1alpha1.DappInventoryEntry{{Dapp: ref("a"), DappID: 7}}
	if id, err := allocateDappID(es, ref("a")); err != nil || id != 7 {
		t.Fatalf("got %d, %v", id, err)
	}
}

func TestAllocateDappIDLowestFree(t *testing.T) {
	es := []cachev1alpha1.DappInventoryEntry{
		{Dapp: ref("a"), DappID: 1}, {Dapp: ref("b"), DappID: 2}, {Dapp: ref("c"), DappID: 4},
	}
	if id, err := allocateDappID(es, ref("new")); err != nil || id != 3 {
		t.Fatalf("got %d, %v", id, err)
	}
}

func TestAllocateDappIDFull(t *testing.T) {
	var es []cachev1alpha1.DappInventoryEntry
	for i := minDappID; i <= maxDappID; i++ {
		es = append(es, cachev1alpha1.DappInventoryEntry{Dapp: ref(string(rune('a'+i%26)) + string(rune('A'+i/26))), DappID: i})
	}
	if _, err := allocateDappID(es, ref("new")); err == nil {
		t.Fatal("expected error")
	}
}

func TestNewTokenHash(t *testing.T) {
	tok, hash, err := newToken()
	if err != nil || hashToken(tok) != hash || len(hash) != 64 {
		t.Fatalf("bad token/hash: %v", err)
	}
	if tok2, _, _ := newToken(); tok2 == tok {
		t.Fatal("tokens equal")
	}
}
