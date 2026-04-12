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
	platformv1alpha1 "github.com/yuvalRipkin/platformpilot-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// DevEnvironmentReconciler reconciles a DevEnvironment object
type DevEnvironmentReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

var tierQuotas = map[string]corev1.ResourceList{
	"small": {
		corev1.ResourceRequestsCPU:    resource.MustParse("2"),
		corev1.ResourceLimitsCPU:      resource.MustParse("4"),
		corev1.ResourceRequestsMemory: resource.MustParse("2Gi"),
		corev1.ResourceLimitsMemory:   resource.MustParse("4Gi"),
		corev1.ResourcePods:           resource.MustParse("10"),
	},
	"medium": {
		corev1.ResourceRequestsCPU:    resource.MustParse("4"),
		corev1.ResourceLimitsCPU:      resource.MustParse("8"),
		corev1.ResourceRequestsMemory: resource.MustParse("8Gi"),
		corev1.ResourceLimitsMemory:   resource.MustParse("16Gi"),
		corev1.ResourcePods:           resource.MustParse("20"),
	},
	"large": {
		corev1.ResourceRequestsCPU:    resource.MustParse("8"),
		corev1.ResourceLimitsCPU:      resource.MustParse("16"),
		corev1.ResourceRequestsMemory: resource.MustParse("16Gi"),
		corev1.ResourceLimitsMemory:   resource.MustParse("32Gi"),
		corev1.ResourcePods:           resource.MustParse("50"),
	},
}

// +kubebuilder:rbac:groups=platform.platformpilot.io,resources=devenvironments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=platform.platformpilot.io,resources=devenvironments/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=platform.platformpilot.io,resources=devenvironments/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups="",resources=resourcequotas,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles;rolebindings,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *DevEnvironmentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Fetch the DevEnvironment resource.
	var devEnv platformv1alpha1.DevEnvironment
	err := r.Client.Get(ctx, req.NamespacedName, &devEnv)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Info("DevEnvironment not found, might have been deleted")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Ensure the Namespace exists; create it if not found.
	namespaceName := devEnv.Spec.Team + "-" + devEnv.Spec.EnvType
	var namespace corev1.Namespace
	err = r.Client.Get(ctx, types.NamespacedName{Name: namespaceName}, &namespace)
	if err != nil {
		if errors.IsNotFound(err) {
			// Namespace not found — create it.
			ns := r.buildNamespace(&devEnv)
			err := r.Client.Create(ctx, ns)
			if err != nil {
				return ctrl.Result{}, err
			}
			log.Info("Created namespace", "name", namespaceName)
		} else {
			// Requeue on transient errors.
			return ctrl.Result{}, err
		}
	}

	// Ensure the RBAC Role exists; create it if not found.
	var rbac rbacv1.Role
	err = r.Client.Get(ctx, types.NamespacedName{
		Name:      namespaceName + "-role",
		Namespace: namespaceName,
	}, &rbac)
	if err != nil {
		if errors.IsNotFound(err) {
			// Role not found — create it.
			rbacRole := r.buildRole(&devEnv)
			err := r.Client.Create(ctx, rbacRole)
			if err != nil {
				return ctrl.Result{}, err
			}
			log.Info("Created rbac role", "name", rbacRole.ObjectMeta.Name)
		} else {
			// Requeue on transient errors.
			return ctrl.Result{}, err
		}
	}

	// Ensure the RoleBinding exists; create it if not found.
	var roleBinding rbacv1.RoleBinding
	err = r.Client.Get(ctx, types.NamespacedName{
		Name:      namespaceName + "-rolebinding",
		Namespace: namespaceName,
	}, &roleBinding)
	if err != nil {
		if errors.IsNotFound(err) {
			// RoleBinding not found — create it.
			roleBinding := r.buildRoleBinding(&devEnv)
			err := r.Client.Create(ctx, roleBinding)
			if err != nil {
				return ctrl.Result{}, err
			}
			log.Info("Created rbac role binding", "name", roleBinding.ObjectMeta.Name)
		} else {
			return ctrl.Result{}, err
		}
	}

	// Ensure the ResourceQuota exists; create it if not found.
	var resourceQuota corev1.ResourceQuota
	err = r.Client.Get(ctx, types.NamespacedName{
		Name:      namespaceName + "-resourcequota",
		Namespace: namespaceName,
	}, &resourceQuota)
	if err != nil {
		if errors.IsNotFound(err) {
			// ResourceQuota not found — create it.
			resourceQuota := r.buildResourceQuota(&devEnv)
			err := r.Client.Create(ctx, resourceQuota)
			if err != nil {
				return ctrl.Result{}, err
			}
			log.Info("Created resourcequota", "name", resourceQuota.ObjectMeta.Name)
		} else {
			return ctrl.Result{}, err
		}
	}

	// Ensure the NetworkPolicy exists; create it if not found.
	var networkPolicy networkingv1.NetworkPolicy
	err = r.Client.Get(ctx, types.NamespacedName{
		Namespace: namespaceName,
		Name:      namespaceName + "-networkpolicy",
	}, &networkPolicy)
	if err != nil {
		if errors.IsNotFound(err) {
			// NetworkPolicy not found — create it.
			networkPolicy := r.buildNetworkPolicy(&devEnv)
			err := r.Client.Create(ctx, networkPolicy)
			if err != nil {
				return ctrl.Result{}, err
			}
			log.Info("Created network policy", "name", networkPolicy.ObjectMeta.Name)
		} else {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *DevEnvironmentReconciler) buildNamespace(devEnv *platformv1alpha1.DevEnvironment) *corev1.Namespace {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: devEnv.Spec.Team + "-" + devEnv.Spec.EnvType,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "platformpilot-operator",
				"app.kubernetes.io/part-of":    "platformpilot",
				"platformpilot.io/team":        devEnv.Spec.Team,
				"platformpilot.io/env-type":    devEnv.Spec.EnvType,
				"platformpilot.io/tier":        devEnv.Spec.Tier,
			},
		},
	}
	return ns
}

func (r *DevEnvironmentReconciler) buildRole(devEnv *platformv1alpha1.DevEnvironment) *rbacv1.Role {
	namespaceName := devEnv.Spec.Team + "-" + devEnv.Spec.EnvType
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      namespaceName + "-role",
			Namespace: namespaceName,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "platformpilot-operator",
				"app.kubernetes.io/part-of":    "platformpilot",
				"platformpilot.io/team":        devEnv.Spec.Team,
				"platformpilot.io/env-type":    devEnv.Spec.EnvType,
			},
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"pods", "services", "persistentvolumeclaims", "configmaps", "secrets"},
				Verbs:     []string{"get", "list", "watch", "create", "update", "delete"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"events"},
				Verbs:     []string{"get", "list", "watch"},
			},
			{
				APIGroups: []string{"apps"},
				Resources: []string{"deployments"},
				Verbs:     []string{"get", "list", "watch", "create", "update", "delete"},
			},
			{
				APIGroups: []string{"networking.k8s.io"},
				Resources: []string{"ingresses"},
				Verbs:     []string{"get", "list", "watch", "create", "update", "delete"},
			},
		},
	}
	ctrl.SetControllerReference(devEnv, role, r.Scheme)
	return role
}

func (r *DevEnvironmentReconciler) buildRoleBinding(devEnv *platformv1alpha1.DevEnvironment) *rbacv1.RoleBinding {
	var namespaceName = devEnv.Spec.Team + "-" + devEnv.Spec.EnvType
	roleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespaceName,
			Name:      namespaceName + "-rolebinding",
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "platformpilot-operator",
				"app.kubernetes.io/part-of":    "platformpilot",
				"platformpilot.io/team":        devEnv.Spec.Team,
				"platformpilot.io/env-type":    devEnv.Spec.EnvType,
				"platformpilot.io/tier":        devEnv.Spec.Tier,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     namespaceName + "-role",
		},
		Subjects: []rbacv1.Subject{
			{
				Kind: "Group",
				Name: devEnv.Spec.Team,
			},
		},
	}
	ctrl.SetControllerReference(devEnv, roleBinding, r.Scheme)
	return roleBinding
}

func (r *DevEnvironmentReconciler) buildResourceQuota(devEnv *platformv1alpha1.DevEnvironment) *corev1.ResourceQuota {
	var namespaceName = devEnv.Spec.Team + "-" + devEnv.Spec.EnvType
	quota := &corev1.ResourceQuota{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespaceName,
			Name:      namespaceName + "-resourcequota",
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "platformpilot-operator",
				"app.kubernetes.io/part-of":    "platformpilot",
				"platformpilot.io/team":        devEnv.Spec.Team,
				"platformpilot.io/env-type":    devEnv.Spec.EnvType,
				"platformpilot.io/tier":        devEnv.Spec.Tier,
			},
		},
		Spec: corev1.ResourceQuotaSpec{
			Hard: tierQuotas[devEnv.Spec.Tier],
		},
	}
	ctrl.SetControllerReference(devEnv, quota, r.Scheme)
	return quota
}

func (r *DevEnvironmentReconciler) buildNetworkPolicy(devEnv *platformv1alpha1.DevEnvironment) *networkingv1.NetworkPolicy {
	var namespaceName = devEnv.Spec.Team + "-" + devEnv.Spec.EnvType
	networkPolicy := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespaceName,
			Name:      namespaceName + "-networkpolicy",
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "platformpilot-operator",
				"app.kubernetes.io/part-of":    "platformpilot",
				"platformpilot.io/team":        devEnv.Spec.Team,
				"platformpilot.io/env-type":    devEnv.Spec.EnvType,
				"platformpilot.io/tier":        devEnv.Spec.Tier,
			},
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeIngress,
			},
			Ingress: []networkingv1.NetworkPolicyIngressRule{
				{
					From: []networkingv1.NetworkPolicyPeer{
						{
							PodSelector: &metav1.LabelSelector{},
						},
					},
				},
			},
		},
	}
	ctrl.SetControllerReference(devEnv, networkPolicy, r.Scheme)
	return networkPolicy
}

// SetupWithManager sets up the controller with the Manager.
func (r *DevEnvironmentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&platformv1alpha1.DevEnvironment{}).
		Named("devenvironment").
		Complete(r)
}
