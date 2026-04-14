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
	"fmt"
	platformv1alpha1 "github.com/yuvalRipkin/platformpilot-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	controllerutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// DevEnvironmentReconciler reconciles a DevEnvironment object
type DevEnvironmentReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

const (
	ConditionNamespaceReady     = "NamespaceReady"
	ConditionRBACReady          = "RBACReady"
	ConditionQuotaReady         = "QuotaReady"
	ConditionNetworkPolicyReady = "NetworkPolicyReady"
)

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

func namespaceName(devEnv *platformv1alpha1.DevEnvironment) string {
	return devEnv.Spec.Team + "-" + devEnv.Spec.EnvType
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

	// deletionTimestamp logic
	deletionTimestamp := devEnv.ObjectMeta.DeletionTimestamp
	if !deletionTimestamp.IsZero() {
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespaceName(&devEnv),
			},
		}
		err := r.Client.Delete(ctx, ns)
		if err != nil && !errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		log.Info("Deleted namespace", "name", ns.ObjectMeta.Name)

		controllerutil.RemoveFinalizer(&devEnv, "platformpilot.io/cleanup")
		err = r.Client.Update(ctx, &devEnv)
		return ctrl.Result{}, err
	}
	if !controllerutil.ContainsFinalizer(&devEnv, "platformpilot.io/cleanup") {
		controllerutil.AddFinalizer(&devEnv, "platformpilot.io/cleanup")
		err := r.Client.Update(ctx, &devEnv)
		if err != nil {
			return ctrl.Result{}, err
		}
	}
	devEnv.Status.Phase = "Provisioning"
	_ = r.Status().Update(ctx, &devEnv)

	// Ensure the Namespace exists; create it if not found.
	namespaceName := namespaceName(&devEnv)
	var namespace corev1.Namespace
	err = r.Client.Get(ctx, types.NamespacedName{Name: namespaceName}, &namespace)
	if err != nil {
		if errors.IsNotFound(err) {
			// Namespace not found — create it.
			ns := r.buildNamespace(&devEnv)
			err = r.Client.Create(ctx, ns)
			if err != nil {
				devEnv.Status.Phase = "Error"
				apimeta.SetStatusCondition(&devEnv.Status.Conditions, metav1.Condition{
					Type:               ConditionNamespaceReady,
					Status:             metav1.ConditionFalse,
					Reason:             "NamespaceBuildError",
					Message:            fmt.Sprintf("Failed to build Namespace for %s: %v", namespaceName, err),
					ObservedGeneration: devEnv.Generation,
				})
				_ = r.Status().Update(ctx, &devEnv)
				return ctrl.Result{}, err
			}
			log.Info("Created namespace", "name", namespaceName)
		} else {
			// Requeue on transient errors.
			return ctrl.Result{}, err
		}
	}
	apimeta.SetStatusCondition(&devEnv.Status.Conditions, metav1.Condition{
		Type:               ConditionNamespaceReady,
		Status:             metav1.ConditionTrue,
		Reason:             "NamespaceProvisioned",
		Message:            "Namespace " + namespaceName + " is provisioned",
		ObservedGeneration: devEnv.Generation,
	})

	// Ensure the RBAC Role exists; create it if not found.
	var rbac rbacv1.Role
	err = r.Client.Get(ctx, types.NamespacedName{
		Name:      namespaceName + "-role",
		Namespace: namespaceName,
	}, &rbac)
	if err != nil {
		if errors.IsNotFound(err) {
			// Role not found — create it.
			rbacRole, err := r.buildRole(&devEnv)
			if err != nil {
				devEnv.Status.Phase = "Error"
				apimeta.SetStatusCondition(&devEnv.Status.Conditions, metav1.Condition{
					Type:               ConditionRBACReady,
					Status:             metav1.ConditionFalse,
					Reason:             "RoleBuildError",
					Message:            fmt.Sprintf("Failed to build Role for %s: %v", namespaceName, err),
					ObservedGeneration: devEnv.Generation,
				})
				_ = r.Status().Update(ctx, &devEnv)
				return ctrl.Result{}, err
			}
			err = r.Client.Create(ctx, rbacRole)
			if err != nil {
				devEnv.Status.Phase = "Error"
				apimeta.SetStatusCondition(&devEnv.Status.Conditions, metav1.Condition{
					Type:               ConditionRBACReady,
					Status:             metav1.ConditionFalse,
					Reason:             "RoleBuildError",
					Message:            fmt.Sprintf("Failed to create Role for %s: %v", namespaceName, err),
					ObservedGeneration: devEnv.Generation,
				})
				_ = r.Status().Update(ctx, &devEnv)
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
			roleBinding, err := r.buildRoleBinding(&devEnv)
			if err != nil {
				devEnv.Status.Phase = "Error"
				apimeta.SetStatusCondition(&devEnv.Status.Conditions, metav1.Condition{
					Type:               ConditionRBACReady,
					Status:             metav1.ConditionFalse,
					Reason:             "RoleBindingBuildError",
					Message:            fmt.Sprintf("Failed to build RoleBinding for %s: %v", namespaceName, err),
					ObservedGeneration: devEnv.Generation,
				})
				_ = r.Status().Update(ctx, &devEnv)
				return ctrl.Result{}, err
			}
			err = r.Client.Create(ctx, roleBinding)
			if err != nil {
				devEnv.Status.Phase = "Error"
				apimeta.SetStatusCondition(&devEnv.Status.Conditions, metav1.Condition{
					Type:               ConditionRBACReady,
					Status:             metav1.ConditionFalse,
					Reason:             "RoleBindingCreationError",
					Message:            fmt.Sprintf("Failed to create RoleBinding for %s: %v", namespaceName, err),
					ObservedGeneration: devEnv.Generation,
				})
				_ = r.Status().Update(ctx, &devEnv)
				return ctrl.Result{}, err
			}
			log.Info("Created rbac role binding", "name", roleBinding.ObjectMeta.Name)
		} else {
			return ctrl.Result{}, err
		}
	}
	apimeta.SetStatusCondition(&devEnv.Status.Conditions, metav1.Condition{
		Type:               ConditionRBACReady,
		Status:             metav1.ConditionTrue,
		Reason:             "RBACProvisioned",
		Message:            "Role and RoleBinding for " + namespaceName + " are provisioned",
		ObservedGeneration: devEnv.Generation,
	})

	// Ensure the ResourceQuota exists; create it if not found.
	var resourceQuota corev1.ResourceQuota
	err = r.Client.Get(ctx, types.NamespacedName{
		Name:      namespaceName + "-resourcequota",
		Namespace: namespaceName,
	}, &resourceQuota)
	if err != nil {
		if errors.IsNotFound(err) {
			// ResourceQuota not found — create it.
			resourceQuota, err := r.buildResourceQuota(&devEnv)
			if err != nil {
				devEnv.Status.Phase = "Error"
				apimeta.SetStatusCondition(&devEnv.Status.Conditions, metav1.Condition{
					Type:               ConditionQuotaReady,
					Status:             metav1.ConditionFalse,
					Reason:             "QuotaBuildError",
					Message:            fmt.Sprintf("Failed to build ResourceQuota for %s: %v", namespaceName, err),
					ObservedGeneration: devEnv.Generation,
				})
				_ = r.Status().Update(ctx, &devEnv)
				return ctrl.Result{}, err
			}
			err = r.Client.Create(ctx, resourceQuota)
			if err != nil {
				devEnv.Status.Phase = "Error"
				apimeta.SetStatusCondition(&devEnv.Status.Conditions, metav1.Condition{
					Type:               ConditionQuotaReady,
					Status:             metav1.ConditionFalse,
					Reason:             "QuotaCreationError",
					Message:            fmt.Sprintf("Failed to create ResourceQuota for %s: %v", namespaceName, err),
					ObservedGeneration: devEnv.Generation,
				})
				_ = r.Status().Update(ctx, &devEnv)
				return ctrl.Result{}, err
			}
			log.Info("Created resourcequota", "name", resourceQuota.ObjectMeta.Name)
		} else {
			return ctrl.Result{}, err
		}
	}
	apimeta.SetStatusCondition(&devEnv.Status.Conditions, metav1.Condition{
		Type:               ConditionQuotaReady,
		Status:             metav1.ConditionTrue,
		Reason:             "QuotaProvisioned",
		Message:            "ResourceQuota for tier " + devEnv.Spec.Tier + " is provisioned",
		ObservedGeneration: devEnv.Generation,
	})

	// Ensure the NetworkPolicy exists. create it if not found.
	var networkPolicy networkingv1.NetworkPolicy
	err = r.Client.Get(ctx, types.NamespacedName{
		Namespace: namespaceName,
		Name:      namespaceName + "-networkpolicy",
	}, &networkPolicy)
	if err != nil {
		if errors.IsNotFound(err) {
			// NetworkPolicy not found — create it.
			networkPolicy, err := r.buildNetworkPolicy(&devEnv)
			if err != nil {
				devEnv.Status.Phase = "Error"
				apimeta.SetStatusCondition(&devEnv.Status.Conditions, metav1.Condition{
					Type:               ConditionNetworkPolicyReady,
					Status:             metav1.ConditionFalse,
					Reason:             "NetworkPolicyBuildingError",
					Message:            fmt.Sprintf("Failed to build NetworkPolicy for %s: %v", namespaceName, err),
					ObservedGeneration: devEnv.Generation,
				})
				_ = r.Status().Update(ctx, &devEnv)
				return ctrl.Result{}, err
			}
			err = r.Client.Create(ctx, networkPolicy)
			if err != nil {
				devEnv.Status.Phase = "Error"
				apimeta.SetStatusCondition(&devEnv.Status.Conditions, metav1.Condition{
					Type:               ConditionNetworkPolicyReady,
					Status:             metav1.ConditionFalse,
					Reason:             "NetworkPolicyCreationError",
					Message:            fmt.Sprintf("Failed to create NetworkPolicy for %s: %v", namespaceName, err),
					ObservedGeneration: devEnv.Generation,
				})
				_ = r.Status().Update(ctx, &devEnv)
				return ctrl.Result{}, err
			}
			log.Info("Created network policy", "name", networkPolicy.ObjectMeta.Name)
		} else {
			return ctrl.Result{}, err
		}
	}
	apimeta.SetStatusCondition(&devEnv.Status.Conditions, metav1.Condition{
		Type:               ConditionNetworkPolicyReady,
		Status:             metav1.ConditionTrue,
		Reason:             "NetworkPolicyProvisioned",
		Message:            "NetworkPolicy for " + namespaceName + " is provisioned",
		ObservedGeneration: devEnv.Generation,
	})
	devEnv.Status.Phase = "Ready"
	err = r.Status().Update(ctx, &devEnv)
	if err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *DevEnvironmentReconciler) buildNamespace(devEnv *platformv1alpha1.DevEnvironment) *corev1.Namespace {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespaceName(devEnv),
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

func (r *DevEnvironmentReconciler) buildRole(devEnv *platformv1alpha1.DevEnvironment) (*rbacv1.Role, error) {
	namespaceName := namespaceName(devEnv)
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
	err := ctrl.SetControllerReference(devEnv, role, r.Scheme)
	if err != nil {
		return nil, fmt.Errorf("failed to set controller reference: %w", err)
	}
	return role, nil
}

func (r *DevEnvironmentReconciler) buildRoleBinding(devEnv *platformv1alpha1.DevEnvironment) (*rbacv1.RoleBinding, error) {
	var namespaceName = namespaceName(devEnv)
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
	err := ctrl.SetControllerReference(devEnv, roleBinding, r.Scheme)
	if err != nil {
		return nil, fmt.Errorf("failed to set controller reference: %w", err)
	}
	return roleBinding, nil
}

func (r *DevEnvironmentReconciler) buildResourceQuota(devEnv *platformv1alpha1.DevEnvironment) (*corev1.ResourceQuota, error) {
	var namespaceName = namespaceName(devEnv)
	quotas, ok := tierQuotas[devEnv.Spec.Tier]
	if !ok {
		return nil, fmt.Errorf("unknown tier: %s", devEnv.Spec.Tier)
	}
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
			Hard: quotas,
		},
	}
	err := ctrl.SetControllerReference(devEnv, quota, r.Scheme)
	if err != nil {
		return nil, fmt.Errorf("failed to set controller reference: %w", err)
	}
	return quota, nil
}

func (r *DevEnvironmentReconciler) buildNetworkPolicy(devEnv *platformv1alpha1.DevEnvironment) (*networkingv1.NetworkPolicy, error) {
	var namespaceName = namespaceName(devEnv)
	udpProtocol := corev1.ProtocolUDP
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
				networkingv1.PolicyTypeEgress,
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
			Egress: []networkingv1.NetworkPolicyEgressRule{
				{
					To: []networkingv1.NetworkPolicyPeer{
						{
							PodSelector: &metav1.LabelSelector{},
						},
					},
				},
				{
					// Allow DNS
					Ports: []networkingv1.NetworkPolicyPort{
						{
							Protocol: &udpProtocol,
							Port:     &intstr.IntOrString{Type: intstr.Int, IntVal: 53},
						},
					},
				},
			},
		},
	}
	err := ctrl.SetControllerReference(devEnv, networkPolicy, r.Scheme)
	if err != nil {
		return nil, fmt.Errorf("failed to set controller reference: %w", err)
	}
	return networkPolicy, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *DevEnvironmentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&platformv1alpha1.DevEnvironment{}).
		Owns(&rbacv1.Role{}).
		Owns(&rbacv1.RoleBinding{}).
		Owns(&networkingv1.NetworkPolicy{}).
		Owns(&corev1.ResourceQuota{}).
		Named("devenvironment").
		Complete(r)
}
