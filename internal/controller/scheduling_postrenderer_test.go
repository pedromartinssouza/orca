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

	sigsyaml "sigs.k8s.io/yaml"

	cachev1alpha1 "github.com/pedromartinssouza/orca/api/v1alpha1"
)

// patchedPodSpec decodes a Kustomize patch's YAML body and returns the
// spec.template.spec map it sets on the target workload.
func patchedPodSpec(t *testing.T, patchYAML string) map[string]interface{} {
	t.Helper()

	var obj map[string]interface{}
	if err := sigsyaml.Unmarshal([]byte(patchYAML), &obj); err != nil {
		t.Fatalf("unmarshaling patch YAML: %v", err)
	}

	spec, ok := obj["spec"].(map[string]interface{})
	if !ok {
		t.Fatalf("patch has no top-level spec: %#v", obj)
	}
	template, ok := spec["template"].(map[string]interface{})
	if !ok {
		t.Fatalf("patch spec has no template: %#v", spec)
	}
	podSpec, ok := template["spec"].(map[string]interface{})
	if !ok {
		t.Fatalf("patch template has no spec: %#v", template)
	}
	return podSpec
}

func TestBuildSchedulingPostRenderer_NodeName(t *testing.T) {
	dappManifest := &cachev1alpha1.DappManifest{
		Spec: cachev1alpha1.DappManifestSpec{
			NodeName: "orca-testbed-worker2",
		},
	}

	renderers, err := buildSchedulingPostRenderer(dappManifest)
	if err != nil {
		t.Fatalf("buildSchedulingPostRenderer returned error: %v", err)
	}
	if len(renderers) != 1 || renderers[0].Kustomize == nil {
		t.Fatalf("expected exactly one Kustomize post-renderer, got %#v", renderers)
	}
	patches := renderers[0].Kustomize.Patches
	if len(patches) == 0 {
		t.Fatalf("expected at least one patch, got none")
	}

	for _, patch := range patches {
		podSpec := patchedPodSpec(t, patch.Patch)
		if got := podSpec["nodeName"]; got != "orca-testbed-worker2" {
			t.Errorf("patch for target %v: nodeName = %v, want %q", patch.Target, got, "orca-testbed-worker2")
		}
	}
}

func TestBuildSchedulingPostRenderer_NodeNameCombinesWithSelectorAndTolerations(t *testing.T) {
	dappManifest := &cachev1alpha1.DappManifest{
		Spec: cachev1alpha1.DappManifestSpec{
			NodeName:     "orca-testbed-worker2",
			NodeSelector: map[string]string{"type": "o-du"},
		},
	}

	renderers, err := buildSchedulingPostRenderer(dappManifest)
	if err != nil {
		t.Fatalf("buildSchedulingPostRenderer returned error: %v", err)
	}
	if len(renderers) != 1 || renderers[0].Kustomize == nil {
		t.Fatalf("expected exactly one Kustomize post-renderer, got %#v", renderers)
	}

	podSpec := patchedPodSpec(t, renderers[0].Kustomize.Patches[0].Patch)
	if got := podSpec["nodeName"]; got != "orca-testbed-worker2" {
		t.Errorf("nodeName = %v, want %q", got, "orca-testbed-worker2")
	}
	nodeSelector, ok := podSpec["nodeSelector"].(map[string]interface{})
	if !ok || nodeSelector["type"] != "o-du" {
		t.Errorf("nodeSelector = %v, want map[type:o-du]", podSpec["nodeSelector"])
	}
}

func TestBuildSchedulingPostRenderer_NoSchedulingFieldsReturnsNil(t *testing.T) {
	dappManifest := &cachev1alpha1.DappManifest{}

	renderers, err := buildSchedulingPostRenderer(dappManifest)
	if err != nil {
		t.Fatalf("buildSchedulingPostRenderer returned error: %v", err)
	}
	if renderers != nil {
		t.Errorf("expected nil post-renderers when no scheduling fields are set, got %#v", renderers)
	}
}
