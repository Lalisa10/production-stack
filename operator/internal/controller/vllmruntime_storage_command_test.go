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
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	productionstackv1alpha1 "production-stack/api/v1alpha1"
)

func newVLLMRuntimeReconcilerForUnitTest(t *testing.T) *VLLMRuntimeReconciler {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("add Kubernetes types to scheme: %v", err)
	}
	if err := productionstackv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add production stack types to scheme: %v", err)
	}

	return &VLLMRuntimeReconciler{Scheme: scheme}
}

func newVLLMRuntimeForUnitTest() *productionstackv1alpha1.VLLMRuntime {
	return &productionstackv1alpha1.VLLMRuntime{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-runtime",
			Namespace: "default",
		},
		Spec: productionstackv1alpha1.VLLMRuntimeSpec{
			Model: productionstackv1alpha1.ModelSpec{ModelURL: "example/model"},
			VLLMConfig: productionstackv1alpha1.VLLMConfig{
				Port: 8000,
			},
			DeploymentConfig: productionstackv1alpha1.DeploymentConfig{
				Replicas: 1,
				Image: productionstackv1alpha1.ImageSpec{
					Registry: "docker.io",
					Name:     "example/vllm:latest",
				},
			},
		},
	}
}

func TestVLLMRuntimeCommand(t *testing.T) {
	reconciler := newVLLMRuntimeReconcilerForUnitTest(t)

	t.Run("uses the legacy command by default", func(t *testing.T) {
		runtime := newVLLMRuntimeForUnitTest()
		deployment := reconciler.deploymentForVLLMRuntime(runtime)

		expected := []string{"/opt/venv/bin/vllm", "serve"}
		actual := deployment.Spec.Template.Spec.Containers[0].Command
		if !reflect.DeepEqual(expected, actual) {
			t.Fatalf("unexpected default command: want %v, got %v", expected, actual)
		}
	})

	t.Run("uses a custom command", func(t *testing.T) {
		runtime := newVLLMRuntimeForUnitTest()
		runtime.Spec.VLLMConfig.Command = []string{"vllm", "serve"}
		deployment := reconciler.deploymentForVLLMRuntime(runtime)

		if !reflect.DeepEqual(runtime.Spec.VLLMConfig.Command,
			deployment.Spec.Template.Spec.Containers[0].Command) {
			t.Fatalf("custom command was not propagated")
		}
	})

	t.Run("detects command changes", func(t *testing.T) {
		runtime := newVLLMRuntimeForUnitTest()
		deployment := reconciler.deploymentForVLLMRuntime(runtime)
		runtime.Spec.VLLMConfig.Command = []string{"vllm", "serve"}

		if !reconciler.deploymentNeedsUpdate(context.Background(), deployment, runtime) {
			t.Fatalf("expected a command change to require a deployment update")
		}
	})
}

func TestVLLMRuntimePersistentVolumeClaimProvisioning(t *testing.T) {
	reconciler := newVLLMRuntimeReconcilerForUnitTest(t)

	t.Run("keeps dynamic provisioning behavior without a selector", func(t *testing.T) {
		runtime := newVLLMRuntimeForUnitTest()
		claim := reconciler.pvcForVLLMRuntime(runtime)

		if claim.Spec.Selector != nil {
			t.Fatalf("expected no selector, got %#v", claim.Spec.Selector)
		}
		if claim.Spec.StorageClassName != nil {
			t.Fatalf("expected the default StorageClass to remain eligible")
		}
	})

	t.Run("selects a classless static volume by labels and expressions", func(t *testing.T) {
		runtime := newVLLMRuntimeForUnitTest()
		runtime.Spec.StorageConfig.Selector = &metav1.LabelSelector{
			MatchLabels: map[string]string{"storage.example.com/model": "llama"},
			MatchExpressions: []metav1.LabelSelectorRequirement{
				{
					Key:      "storage.example.com/tier",
					Operator: metav1.LabelSelectorOpIn,
					Values:   []string{"fast", "archive"},
				},
			},
		}

		claim := reconciler.pvcForVLLMRuntime(runtime)
		if !reflect.DeepEqual(runtime.Spec.StorageConfig.Selector, claim.Spec.Selector) {
			t.Fatalf("selector was not propagated: want %#v, got %#v",
				runtime.Spec.StorageConfig.Selector, claim.Spec.Selector)
		}
		if claim.Spec.StorageClassName == nil || *claim.Spec.StorageClassName != "" {
			t.Fatalf("expected an explicit empty storageClassName, got %#v",
				claim.Spec.StorageClassName)
		}
	})

	t.Run("selects a static volume in the configured storage class", func(t *testing.T) {
		runtime := newVLLMRuntimeForUnitTest()
		runtime.Spec.StorageConfig.StorageClassName = "manual"
		runtime.Spec.StorageConfig.Selector = &metav1.LabelSelector{
			MatchLabels: map[string]string{"storage.example.com/model": "llama"},
		}

		claim := reconciler.pvcForVLLMRuntime(runtime)
		if claim.Spec.StorageClassName == nil || *claim.Spec.StorageClassName != "manual" {
			t.Fatalf("expected storage class manual, got %#v", claim.Spec.StorageClassName)
		}
	})
}
