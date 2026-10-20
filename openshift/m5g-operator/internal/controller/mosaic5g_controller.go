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
	"slices"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	mosaic5gv1alpha1 "github.com/kubedge/kube5g/openshift/m5g-operator/api/v1alpha1"
)

const (
	conditionAvailable = "Available"

	mysqlWait = 10 * time.Second
	cnWait    = 30 * time.Second
	ranWait   = 10 * time.Second
)

// Mosaic5gReconciler reconciles a Mosaic5g object
type Mosaic5gReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// OpenShift is true when the cluster serves the security.openshift.io
	// API. The pods' ServiceAccount is then bound to the privileged SCC.
	OpenShift bool
}

// +kubebuilder:rbac:groups=mosaic5g.com,resources=mosaic5gs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=mosaic5g.com,resources=mosaic5gs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=mosaic5g.com,resources=mosaic5gs/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=configmaps;services;serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles,verbs=bind,resourceNames=system:openshift:scc:privileged

// Reconcile creates MySQL, oai-cn and oai-ran in that order, each one only
// once the previous one is ready. Configuration changes are rolled out by
// changing a hash annotation on the pod templates.
func (r *Mosaic5gReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	m := &mosaic5gv1alpha1.Mosaic5g{}
	if err := r.Get(ctx, req.NamespacedName, m); err != nil {
		// Owned objects are garbage collected through their owner references.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if err := r.ensureServiceAccount(ctx, m); err != nil {
		return ctrl.Result{}, err
	}

	configHash, err := r.ensureConfigMap(ctx, m)
	if err != nil {
		return ctrl.Result{}, err
	}

	mysql, err := r.ensureDeployment(ctx, m, mysqlName, 1, mysqlSelector(), "", mysqlPodSpec(m))
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := r.ensureService(ctx, m, mysqlName, mysqlSelector(), mysqlServicePorts()); err != nil {
		return ctrl.Result{}, err
	}
	if !ready(mysql) {
		log.Info("Waiting for MySQL to be ready")
		return r.waiting(ctx, m, "WaitingForMySQL", mysqlWait)
	}

	size := ptr.Deref(m.Spec.Size, 1)
	cn, err := r.ensureDeployment(ctx, m, cnDeploymentName(m), size, cnSelector(), configHash, cnPodSpec(m))
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := r.ensureService(ctx, m, cnServiceName, cnSelector(), cnServicePorts()); err != nil {
		return ctrl.Result{}, err
	}
	if !ready(cn) {
		log.Info("Waiting for oai-cn to be ready")
		return r.waiting(ctx, m, "WaitingForCN", cnWait)
	}

	ran, err := r.ensureDeployment(ctx, m, ranDeploymentName(m), size, ranSelector(m), configHash, ranPodSpec(m))
	if err != nil {
		return ctrl.Result{}, err
	}

	if err := r.deleteStaleDeployments(ctx, m); err != nil {
		return ctrl.Result{}, err
	}

	if !ready(ran) {
		log.Info("Waiting for oai-ran to be ready")
		return r.waiting(ctx, m, "WaitingForRAN", ranWait)
	}
	return ctrl.Result{}, r.updateStatus(ctx, m, metav1.ConditionTrue, "Ready", "MySQL, oai-cn and oai-ran are ready")
}

func ready(dep *appsv1.Deployment) bool {
	return dep != nil && dep.Status.ReadyReplicas > 0
}

// waiting records why the reconcile is not done yet and requeues.
func (r *Mosaic5gReconciler) waiting(ctx context.Context, m *mosaic5gv1alpha1.Mosaic5g, reason string, after time.Duration) (ctrl.Result, error) {
	if err := r.updateStatus(ctx, m, metav1.ConditionFalse, reason, "Waiting for the previous component to become ready"); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: after}, nil
}

func (r *Mosaic5gReconciler) ensureServiceAccount(ctx context.Context, m *mosaic5gv1alpha1.Mosaic5g) error {
	sa := &corev1.ServiceAccount{Name: serviceAccountName, Namespace: m.Namespace}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, sa, func() error {
		sa.Labels = podLabels(m, nil)
		return controllerutil.SetControllerReference(m, sa, r.Scheme)
	}); err != nil {
		return fmt.Errorf("reconciling ServiceAccount: %w", err)
	}
	if !r.OpenShift {
		return nil
	}
	rb := &rbacv1.RoleBinding{Name: sccRoleBindingName, Namespace: m.Namespace}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, rb, func() error {
		mutateSCCRoleBinding(rb, m)
		return controllerutil.SetControllerReference(m, rb, r.Scheme)
	}); err != nil {
		return fmt.Errorf("reconciling privileged SCC RoleBinding: %w", err)
	}
	return nil
}

// ensureConfigMap writes conf.yaml and returns its hash.
func (r *Mosaic5gReconciler) ensureConfigMap(ctx context.Context, m *mosaic5gv1alpha1.Mosaic5g) (string, error) {
	data, hash, err := renderConfig(m)
	if err != nil {
		return "", fmt.Errorf("rendering %s: %w", configFileName, err)
	}
	cm := &corev1.ConfigMap{Name: configMapName, Namespace: m.Namespace}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, cm, func() error {
		cm.Labels = podLabels(m, nil)
		cm.Data = map[string]string{configFileName: data}
		return controllerutil.SetControllerReference(m, cm, r.Scheme)
	}); err != nil {
		return "", fmt.Errorf("reconciling ConfigMap: %w", err)
	}
	return hash, nil
}

func (r *Mosaic5gReconciler) ensureDeployment(ctx context.Context, m *mosaic5gv1alpha1.Mosaic5g, name string, replicas int32,
	selector map[string]string, configHash string, podSpec corev1.PodSpec) (*appsv1.Deployment, error) {
	dep := &appsv1.Deployment{Name: name, Namespace: m.Namespace}
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, dep, func() error {
		mutateDeployment(dep, m, replicas, selector, configHash, podSpec)
		return controllerutil.SetControllerReference(m, dep, r.Scheme)
	})
	if err != nil {
		return nil, fmt.Errorf("reconciling Deployment %s: %w", name, err)
	}
	if op != controllerutil.OperationResultNone {
		logf.FromContext(ctx).Info("Deployment reconciled", "name", name, "operation", op)
	}
	return dep, nil
}

func (r *Mosaic5gReconciler) ensureService(ctx context.Context, m *mosaic5gv1alpha1.Mosaic5g, name string,
	selector map[string]string, ports []corev1.ServicePort) error {
	svc := &corev1.Service{Name: name, Namespace: m.Namespace}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		mutateHeadlessService(svc, m, selector, ports)
		return controllerutil.SetControllerReference(m, svc, r.Scheme)
	}); err != nil {
		return fmt.Errorf("reconciling Service %s: %w", name, err)
	}
	return nil
}

// deleteStaleDeployments removes Deployments owned by m that are no longer
// desired, e.g. the old oai-cn Deployment after mmeDomainName changed.
func (r *Mosaic5gReconciler) deleteStaleDeployments(ctx context.Context, m *mosaic5gv1alpha1.Mosaic5g) error {
	desired := []string{mysqlName, cnDeploymentName(m), ranDeploymentName(m)}
	list := &appsv1.DeploymentList{}
	if err := r.List(ctx, list, client.InNamespace(m.Namespace), client.MatchingLabels{crLabel: m.Name}); err != nil {
		return fmt.Errorf("listing Deployments: %w", err)
	}
	for i := range list.Items {
		dep := &list.Items[i]
		if slices.Contains(desired, dep.Name) || !metav1.IsControlledBy(dep, m) {
			continue
		}
		logf.FromContext(ctx).Info("Deleting stale Deployment", "name", dep.Name)
		if err := r.Delete(ctx, dep); client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("deleting stale Deployment %s: %w", dep.Name, err)
		}
	}
	return nil
}

// updateStatus refreshes status.nodes and the Available condition.
func (r *Mosaic5gReconciler) updateStatus(ctx context.Context, m *mosaic5gv1alpha1.Mosaic5g,
	status metav1.ConditionStatus, reason, message string) error {
	pods := &corev1.PodList{}
	if err := r.List(ctx, pods, client.InNamespace(m.Namespace), client.MatchingLabels{crLabel: m.Name}); err != nil {
		return fmt.Errorf("listing pods: %w", err)
	}
	names := make([]string, 0, len(pods.Items))
	for _, p := range pods.Items {
		names = append(names, p.Name)
	}
	slices.Sort(names)

	orig := m.DeepCopy()
	m.Status.Nodes = names
	meta.SetStatusCondition(&m.Status.Conditions, metav1.Condition{
		Type:               conditionAvailable,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: m.Generation,
	})
	if err := r.Status().Patch(ctx, m, client.MergeFrom(orig)); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("updating status: %w", err)
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *Mosaic5gReconciler) SetupWithManager(mgr ctrl.Manager) error {
	b := ctrl.NewControllerManagedBy(mgr).
		For(&mosaic5gv1alpha1.Mosaic5g{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.ServiceAccount{})
	if r.OpenShift {
		b = b.Owns(&rbacv1.RoleBinding{})
	}
	return b.Named("mosaic5g").Complete(r)
}

// IsOpenShift reports whether the cluster serves SecurityContextConstraints.
func IsOpenShift(mapper meta.RESTMapper) bool {
	_, err := mapper.RESTMapping(schema.GroupKind{Group: "security.openshift.io", Kind: "SecurityContextConstraints"})
	return err == nil
}
