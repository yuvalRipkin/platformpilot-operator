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

// Package controller implements the DevEnvironment reconciler.
package controller

import (
	"context"
	"fmt"
	"reflect"
	"time"

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

// Condition type constants for DevEnvironment status.
const (
	ConditionNamespaceReady     = "NamespaceReady"
	ConditionRoleReady          = "RoleReady"
	ConditionRoleBindingReady   = "RoleBindingReady"
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
// +kubebuilder:rbac:groups="",resources=resourcequotas,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles;rolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *DevEnvironmentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	var devEnv platformv1alpha1.DevEnvironment
	if err := r.Get(ctx, req.NamespacedName, &devEnv); err != nil {
		if errors.IsNotFound(err) {
			log.Info("DevEnvironment not found, might have been deleted")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	original := devEnv.DeepCopy()
	// Use a detached context for the status patch so it is not cancelled during
	// operator shutdown before the write completes.
	defer func() {
		if !reflect.DeepEqual(original.Status, devEnv.Status) {
			patchCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := r.Status().Patch(patchCtx, &devEnv, client.MergeFrom(original)); err != nil {
				log.Error(err, "failed to patch status")
			}
		}
	}()

	if !devEnv.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, &devEnv)
	}
	if !controllerutil.ContainsFinalizer(&devEnv, "platformpilot.io/cleanup") {
		controllerutil.AddFinalizer(&devEnv, "platformpilot.io/cleanup")
		if err := r.Update(ctx, &devEnv); err != nil {
			return ctrl.Result{}, err
		}
		// Return and requeue so the next pass starts from a clean, freshly-fetched object.
		return ctrl.Result{Requeue: true}, nil
	}

	devEnv.Status.Phase = "Provisioning"

	for _, step := range []func(context.Context, *platformv1alpha1.DevEnvironment) (ctrl.Result, error){
		r.reconcileNamespace,
		r.reconcileRole,
		r.reconcileRoleBinding,
		r.reconcileResourceQuota,
		r.reconcileNetworkPolicy,
	} {
		if result, err := step(ctx, &devEnv); err != nil || !result.IsZero() {
			return result, err
		}
	}

	devEnv.Status.Phase = "Ready"
	return ctrl.Result{}, nil
}

func (r *DevEnvironmentReconciler) handleDeletion(ctx context.Context, devEnv *platformv1alpha1.DevEnvironment) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespaceName(devEnv)}}
	if err := r.Delete(ctx, ns); err != nil && !errors.IsNotFound(err) {
		return ctrl.Result{}, err
	}
	log.Info("Deleted namespace", "name", ns.Name)
	controllerutil.RemoveFinalizer(devEnv, "platformpilot.io/cleanup")
	return ctrl.Result{}, r.Update(ctx, devEnv)
}

// setCondition updates or appends a status condition on the DevEnvironment.
// ObservedGeneration is always set to the current devEnv.Generation.
func setCondition(devEnv *platformv1alpha1.DevEnvironment, condType string, status metav1.ConditionStatus, reason, message string) {
	apimeta.SetStatusCondition(&devEnv.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: devEnv.Generation,
	})
}

func (r *DevEnvironmentReconciler) reconcileNamespace(ctx context.Context, devEnv *platformv1alpha1.DevEnvironment) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	nsName := namespaceName(devEnv)

	var namespace corev1.Namespace
	err := r.Get(ctx, types.NamespacedName{Name: nsName}, &namespace)
	switch {
	case errors.IsNotFound(err):
		if err := r.Create(ctx, r.buildNamespace(devEnv)); err != nil {
			devEnv.Status.Phase = "Error"
			setCondition(devEnv, ConditionNamespaceReady, metav1.ConditionFalse, "NamespaceCreationError",
				fmt.Sprintf("failed to create namespace %s: %v", nsName, err))
			return ctrl.Result{}, err
		}
		log.Info("Created namespace", "name", nsName)
		// Requeue to refetch and verify Phase on next pass before declaring Ready.
		return ctrl.Result{Requeue: true}, nil
	case err != nil:
		return ctrl.Result{}, err
	case namespace.Status.Phase == corev1.NamespaceTerminating:
		log.Info("Namespace is terminating, requeueing", "name", nsName)
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	setCondition(devEnv, ConditionNamespaceReady, metav1.ConditionTrue, "NamespaceProvisioned",
		"Namespace "+nsName+" is provisioned")
	return ctrl.Result{}, nil
}
func (r *DevEnvironmentReconciler) reconcileRole(ctx context.Context, devEnv *platformv1alpha1.DevEnvironment) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	nsName := namespaceName(devEnv)

	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      nsName + "-role",
			Namespace: nsName,
		},
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, role, func() error {
		return r.mutateRole(devEnv, role)
	})
	if err != nil {
		devEnv.Status.Phase = "Error"
		setCondition(devEnv, ConditionRoleReady, metav1.ConditionFalse, "RoleReconcileError",
			fmt.Sprintf("failed to reconcile role for %s: %v", nsName, err))
		return ctrl.Result{}, err
	}
	if op != controllerutil.OperationResultNone {
		log.Info("role reconciled", "name", role.Name, "operation", op)
	}

	setCondition(devEnv, ConditionRoleReady, metav1.ConditionTrue, "RoleProvisioned",
		"Role for "+nsName+" is provisioned")
	return ctrl.Result{}, nil
}

func (r *DevEnvironmentReconciler) mutateRole(devEnv *platformv1alpha1.DevEnvironment, role *rbacv1.Role) error {
	role.Labels = map[string]string{
		"app.kubernetes.io/managed-by": "platformpilot-operator",
		"app.kubernetes.io/part-of":    "platformpilot",
		"platformpilot.io/team":        devEnv.Spec.Team,
		"platformpilot.io/env-type":    devEnv.Spec.EnvType,
	}
	role.Rules = []rbacv1.PolicyRule{
		{
			APIGroups: []string{""},
			Resources: []string{"pods"},
			Verbs:     []string{"get", "list", "watch"},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"pods/log"},
			Verbs:     []string{"get"},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"pods/exec", "pods/portforward"},
			Verbs:     []string{"create"},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"services", "configmaps", "secrets", "persistentvolumeclaims"},
			Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
		},
		{
			APIGroups: []string{"apps"},
			Resources: []string{"deployments", "statefulsets", "replicasets"},
			Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
		},
		{
			APIGroups: []string{"networking.k8s.io"},
			Resources: []string{"ingresses"},
			Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"events"},
			Verbs:     []string{"get", "list", "watch"},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"resourcequotas", "limitranges"},
			Verbs:     []string{"get", "list", "watch"},
		},
	}
	return ctrl.SetControllerReference(devEnv, role, r.Scheme)
}

func (r *DevEnvironmentReconciler) reconcileRoleBinding(ctx context.Context, devEnv *platformv1alpha1.DevEnvironment) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	nsName := namespaceName(devEnv)
	roleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      nsName + "-rolebinding",
			Namespace: nsName,
		},
	}
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, roleBinding, func() error {
		return r.mutateRoleBinding(devEnv, roleBinding)
	})
	if err != nil {
		devEnv.Status.Phase = "Error"
		setCondition(devEnv, ConditionRoleBindingReady, metav1.ConditionFalse, "RoleBindingReconcileError",
			fmt.Sprintf("failed to reconcile rolebinding for %s: %v", nsName, err))
		return ctrl.Result{}, err
	}
	if op != controllerutil.OperationResultNone {
		log.Info("rolebinding reconciled", "name", roleBinding.Name, "operation", op)
	}
	setCondition(devEnv, ConditionRoleBindingReady, metav1.ConditionTrue, "RoleBindingProvisioned",
		"RoleBinding for "+nsName+" is provisioned")
	return ctrl.Result{}, nil
}
func (r *DevEnvironmentReconciler) mutateRoleBinding(devEnv *platformv1alpha1.DevEnvironment, roleBinding *rbacv1.RoleBinding) error {
	nsName := namespaceName(devEnv)
	roleBinding.Labels = map[string]string{
		"app.kubernetes.io/managed-by": "platformpilot-operator",
		"app.kubernetes.io/part-of":    "platformpilot",
		"platformpilot.io/team":        devEnv.Spec.Team,
		"platformpilot.io/env-type":    devEnv.Spec.EnvType,
		"platformpilot.io/tier":        devEnv.Spec.Tier,
	}

	if roleBinding.CreationTimestamp.IsZero() {
		roleBinding.RoleRef = rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     nsName + "-role",
		}
	}

	roleBinding.Subjects = []rbacv1.Subject{
		{
			APIGroup: rbacv1.GroupName,
			Kind:     "Group",
			Name:     devEnv.Spec.Team,
		},
	}

	return ctrl.SetControllerReference(devEnv, roleBinding, r.Scheme)
}
func (r *DevEnvironmentReconciler) reconcileResourceQuota(ctx context.Context, devEnv *platformv1alpha1.DevEnvironment) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	nsName := namespaceName(devEnv)
	resourceQuota := &corev1.ResourceQuota{
		ObjectMeta: metav1.ObjectMeta{
			Name:      nsName + "-resourcequota",
			Namespace: nsName,
		},
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, resourceQuota, func() error {
		return r.mutateResourceQuota(devEnv, resourceQuota)
	})
	if err != nil {
		devEnv.Status.Phase = "Error"
		setCondition(devEnv, ConditionRoleReady, metav1.ConditionFalse, "QuotaReconcileError",
			fmt.Sprintf("failed to reconcile resourcequota for %s: %v", nsName, err))
		return ctrl.Result{}, err
	}
	if op != controllerutil.OperationResultNone {
		log.Info("resourcequota reconciled", "name", resourceQuota.Name, "operation", op)
	}
	setCondition(devEnv, ConditionQuotaReady, metav1.ConditionTrue, "QuotaProvisioned",
		"ResourceQuota for tier "+devEnv.Spec.Tier+" is provisioned")
	return ctrl.Result{}, nil
}

func (r *DevEnvironmentReconciler) mutateResourceQuota(devEnv *platformv1alpha1.DevEnvironment, resourceQuota *corev1.ResourceQuota) error {
	quotas, ok := tierQuotas[devEnv.Spec.Tier]
	if !ok {
		return fmt.Errorf("unknown tier: %s", devEnv.Spec.Tier)
	}

	resourceQuota.Labels = map[string]string{
		"app.kubernetes.io/managed-by": "platformpilot-operator",
		"app.kubernetes.io/part-of":    "platformpilot",
		"platformpilot.io/team":        devEnv.Spec.Team,
		"platformpilot.io/env-type":    devEnv.Spec.EnvType,
		"platformpilot.io/tier":        devEnv.Spec.Tier,
	}

	resourceQuota.Spec.Hard = quotas

	return ctrl.SetControllerReference(devEnv, resourceQuota, r.Scheme)
}

func (r *DevEnvironmentReconciler) reconcileNetworkPolicy(ctx context.Context, devEnv *platformv1alpha1.DevEnvironment) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	nsName := namespaceName(devEnv)

	networkPolicy := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      nsName + "-networkpolicy",
			Namespace: nsName,
		},
	}
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, networkPolicy, func() error {
		return r.mutateNetworkPolicy(devEnv, networkPolicy)
	})
	if err != nil {
		devEnv.Status.Phase = "Error"
		setCondition(devEnv, ConditionNetworkPolicyReady, metav1.ConditionFalse, "NetworkPolicyReconcileError",
			fmt.Sprintf("failed to reconcile networkpolicy for %s: %v", nsName, err))
		return ctrl.Result{}, err
	}
	if op != controllerutil.OperationResultNone {
		log.Info("networkpolicy reconciled", "name", networkPolicy.Name, "operation", op)
	}

	setCondition(devEnv, ConditionNetworkPolicyReady, metav1.ConditionTrue, "NetworkPolicyProvisioned",
		"NetworkPolicy for "+nsName+" is provisioned")
	return ctrl.Result{}, nil
}

func (r *DevEnvironmentReconciler) mutateNetworkPolicy(devEnv *platformv1alpha1.DevEnvironment, np *networkingv1.NetworkPolicy) error {
	udpProtocol := corev1.ProtocolUDP
	np.Labels = map[string]string{
		"app.kubernetes.io/managed-by": "platformpilot-operator",
		"app.kubernetes.io/part-of":    "platformpilot",
		"platformpilot.io/team":        devEnv.Spec.Team,
		"platformpilot.io/env-type":    devEnv.Spec.EnvType,
		"platformpilot.io/tier":        devEnv.Spec.Tier,
	}
	np.Spec = networkingv1.NetworkPolicySpec{
		PodSelector: metav1.LabelSelector{},
		PolicyTypes: []networkingv1.PolicyType{
			networkingv1.PolicyTypeIngress,
			networkingv1.PolicyTypeEgress,
		},
		Ingress: []networkingv1.NetworkPolicyIngressRule{
			{From: []networkingv1.NetworkPolicyPeer{{PodSelector: &metav1.LabelSelector{}}}},
		},
		Egress: []networkingv1.NetworkPolicyEgressRule{
			{To: []networkingv1.NetworkPolicyPeer{{PodSelector: &metav1.LabelSelector{}}}},
			{
				// Allow DNS
				Ports: []networkingv1.NetworkPolicyPort{
					{Protocol: &udpProtocol, Port: &intstr.IntOrString{Type: intstr.Int, IntVal: 53}},
				},
			},
		},
	}
	return ctrl.SetControllerReference(devEnv, np, r.Scheme)
}

func (r *DevEnvironmentReconciler) buildNamespace(devEnv *platformv1alpha1.DevEnvironment) *corev1.Namespace {
	nsName := namespaceName(devEnv)
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: nsName,
			Labels: map[string]string{
				"pod-security.kubernetes.io/enforce":         "restricted",
				"pod-security.kubernetes.io/enforce-version": "latest",
				"app.kubernetes.io/managed-by":               "platformpilot-operator",
				"app.kubernetes.io/part-of":                  "platformpilot",
				"platformpilot.io/team":                      devEnv.Spec.Team,
				"platformpilot.io/env-type":                  devEnv.Spec.EnvType,
				"platformpilot.io/tier":                      devEnv.Spec.Tier,
			},
		},
	}
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
