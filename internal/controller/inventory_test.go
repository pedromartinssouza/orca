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
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"

	cachev1alpha1 "github.com/pedromartinssouza/orca/api/v1alpha1"
)

func ref(n string) cachev1alpha1.DappRef { return cachev1alpha1.DappRef{Namespace: "ns", Name: n} }

func TestAllocateDappIDReturnsExisting(t *testing.T) {
	es := []cachev1alpha1.DappInventoryEntry{{Dapp: ref("a"), DappID: 7}}
	if id, err := allocateDappID(es, nil, ref("a")); err != nil || id != 7 {
		t.Fatalf("got %d, %v", id, err)
	}
}

func TestAllocateDappIDLowestFree(t *testing.T) {
	es := []cachev1alpha1.DappInventoryEntry{
		{Dapp: ref("a"), DappID: 1}, {Dapp: ref("b"), DappID: 2}, {Dapp: ref("c"), DappID: 4},
	}
	if id, err := allocateDappID(es, nil, ref("new")); err != nil || id != 3 {
		t.Fatalf("got %d, %v", id, err)
	}
}

func TestAllocateDappIDFull(t *testing.T) {
	var es []cachev1alpha1.DappInventoryEntry
	for i := minDappID; i <= maxDappID; i++ {
		es = append(es, cachev1alpha1.DappInventoryEntry{Dapp: ref(string(rune('a'+i%26)) + string(rune('A'+i/26))), DappID: i})
	}
	if _, err := allocateDappID(es, nil, ref("new")); err == nil {
		t.Fatal("expected error")
	}
}

func TestAllocateDappIDSkipsIDsInStatus(t *testing.T) {
	es := []cachev1alpha1.DappInventoryEntry{{Dapp: ref("a"), DappID: 1}}
	for _, st := range []cachev1alpha1.DappInventoryEntryStatus{
		{DappID: 2, Advertised: true},
		{DappID: 2, Registered: true},
		{DappID: 2}, // add in flight or not yet withdrawn: presence alone holds the ID
	} {
		if id, err := allocateDappID(es, []cachev1alpha1.DappInventoryEntryStatus{st}, ref("new")); err != nil || id != 3 {
			t.Fatalf("%+v: got %d, %v", st, id, err)
		}
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

func TestTokenSecretNameTruncatesToValidName(t *testing.T) {
	// The cut lands right after a '.', which must be trimmed to keep a valid subdomain.
	name := strings.Repeat("a", 236) + ".b" + strings.Repeat("c", 20)
	dm := &cachev1alpha1.DappManifest{ObjectMeta: metav1.ObjectMeta{Namespace: "x", Name: name}}
	got := tokenSecretName(dm, strings.Repeat("f", 64))
	if errs := validation.IsDNS1123Subdomain(got); len(errs) > 0 || !strings.HasSuffix(got, "-e3-ffffffffff") {
		t.Fatalf("%q: %v", got, errs)
	}
}
