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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	platformv1alpha1 "github.com/yuvalRipkin/platformpilot-operator/api/v1alpha1"
)

// reconcileUntilDone drives the reconciler until it returns a zero result with no error,
// or until maxPasses is exhausted. This is necessary because several steps (finalizer add,
// namespace create) return Requeue before the CR reaches Ready.
func reconcileUntilDone(ctx context.Context, r *DevEnvironmentReconciler, req reconcile.Request, maxPasses int) error {
	for i := 0; i < maxPasses; i++ {
		result, err := r.Reconcile(ctx, req)
		if err != nil {
			return err
		}
		if result.IsZero() {
			return nil
		}
	}
	return nil
}

func newReconciler() *DevEnvironmentReconciler {
	return &DevEnvironmentReconciler{
		Client: k8sClient,
		Scheme: k8sClient.Scheme(),
	}
}

func makeDevEnv(name, team, envType, tier string) *platformv1alpha1.DevEnvironment {
	return &platformv1alpha1.DevEnvironment{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: platformv1alpha1.DevEnvironmentSpec{
			Team:    team,
			EnvType: envType,
			Tier:    tier,
		},
	}
}

var _ = Describe("DevEnvironment Controller", func() {
	ctx := context.Background()

	// Each Context uses a unique team name to avoid namespace collisions between tests:
	// envtest leaves namespaces in a Terminating state after deletion, and the reconciler
	// waits for them to disappear before re-provisioning.

	Context("basic reconcile", func() {
		const resourceName = "test-basic"
		nn := types.NamespacedName{Name: resourceName}

		BeforeEach(func() {
			By("creating the DevEnvironment CR")
			err := k8sClient.Get(ctx, nn, &platformv1alpha1.DevEnvironment{})
			if errors.IsNotFound(err) {
				Expect(k8sClient.Create(ctx, makeDevEnv(resourceName, "alpha", "dev", "small"))).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &platformv1alpha1.DevEnvironment{}
			Expect(k8sClient.Get(ctx, nn, resource)).To(Succeed())
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
			// Reconcile so the finalizer is removed and the CR is fully gone before the next test.
			Expect(reconcileUntilDone(ctx, newReconciler(), reconcile.Request{NamespacedName: nn}, 5)).To(Succeed())
		})

		It("should reach Ready and provision all sub-resources", func() {
			Expect(reconcileUntilDone(ctx, newReconciler(), reconcile.Request{NamespacedName: nn}, 10)).To(Succeed())

			result := &platformv1alpha1.DevEnvironment{}
			Expect(k8sClient.Get(ctx, nn, result)).To(Succeed())
			Expect(result.Status.Phase).To(Equal("Ready"))

			nsName := "alpha-dev"

			By("namespace exists")
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: nsName}, &corev1.Namespace{})).To(Succeed())

			By("Role exists")
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: nsName + "-role", Namespace: nsName}, &rbacv1.Role{})).To(Succeed())

			By("RoleBinding exists")
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: nsName + "-rolebinding", Namespace: nsName}, &rbacv1.RoleBinding{})).To(Succeed())

			By("ResourceQuota exists")
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: nsName + "-resourcequota", Namespace: nsName}, &corev1.ResourceQuota{})).To(Succeed())

			By("NetworkPolicy exists")
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: nsName + "-networkpolicy", Namespace: nsName}, &networkingv1.NetworkPolicy{})).To(Succeed())
		})
	})

	Context("idempotency", func() {
		const resourceName = "test-idempotent"
		nn := types.NamespacedName{Name: resourceName}

		BeforeEach(func() {
			By("creating the DevEnvironment CR")
			err := k8sClient.Get(ctx, nn, &platformv1alpha1.DevEnvironment{})
			if errors.IsNotFound(err) {
				Expect(k8sClient.Create(ctx, makeDevEnv(resourceName, "beta", "dev", "small"))).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &platformv1alpha1.DevEnvironment{}
			Expect(k8sClient.Get(ctx, nn, resource)).To(Succeed())
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
			Expect(reconcileUntilDone(ctx, newReconciler(), reconcile.Request{NamespacedName: nn}, 5)).To(Succeed())
		})

		It("should remain Ready after a second reconcile pass", func() {
			req := reconcile.Request{NamespacedName: nn}
			Expect(reconcileUntilDone(ctx, newReconciler(), req, 10)).To(Succeed())
			Expect(reconcileUntilDone(ctx, newReconciler(), req, 10)).To(Succeed())

			result := &platformv1alpha1.DevEnvironment{}
			Expect(k8sClient.Get(ctx, nn, result)).To(Succeed())
			Expect(result.Status.Phase).To(Equal("Ready"))
		})
	})
})
