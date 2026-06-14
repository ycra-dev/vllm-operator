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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	llmv1alpha1 "github.com/youngcheor/vllm-operator/api/v1alpha1"
)

// LLMReconciler reconciles a LLM object
type LLMReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=llm.vllm-operator.io,resources=llms,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=llm.vllm-operator.io,resources=llms/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=llm.vllm-operator.io,resources=llms/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete

// Reconcile drives the cluster state toward the desired state described by an
// LLM resource: it ensures the backing Deployment and Service exist and keeps
// the resource status up to date.
func (r *LLMReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var llm llmv1alpha1.LLM
	if err := r.Get(ctx, req.NamespacedName, &llm); err != nil {
		// Ignore not-found: the resource was deleted and owned objects are
		// garbage-collected via owner references.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if err := r.reconcilePVC(ctx, &llm); err != nil {
		log.Error(err, "Failed to reconcile model cache PVC")
		return r.markDegraded(ctx, &llm, "PVCError", err)
	}

	if err := r.reconcileDeployment(ctx, &llm); err != nil {
		log.Error(err, "Failed to reconcile Deployment")
		return r.markDegraded(ctx, &llm, "DeploymentError", err)
	}

	if err := r.reconcileService(ctx, &llm); err != nil {
		log.Error(err, "Failed to reconcile Service")
		return r.markDegraded(ctx, &llm, "ServiceError", err)
	}

	if err := r.updateStatus(ctx, &llm); err != nil {
		log.Error(err, "Failed to update LLM status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// reconcilePVC ensures the model cache PersistentVolumeClaim exists when caching
// is enabled. A PVC's spec is largely immutable after creation, so it is created
// once and otherwise left untouched.
func (r *LLMReconciler) reconcilePVC(ctx context.Context, llm *llmv1alpha1.LLM) error {
	if llm.Spec.ModelCache == nil {
		return nil
	}
	log := logf.FromContext(ctx)

	pvc := &corev1.PersistentVolumeClaim{}
	key := client.ObjectKey{Name: pvcName(llm), Namespace: llm.Namespace}
	err := r.Get(ctx, key, pvc)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}

	pvc = buildPVC(llm)
	if err := controllerutil.SetControllerReference(llm, pvc, r.Scheme); err != nil {
		return err
	}
	if err := r.Create(ctx, pvc); err != nil {
		return err
	}
	log.Info("Created model cache PVC", "name", pvc.Name)
	return nil
}

// reconcileDeployment creates or updates the vLLM Deployment.
func (r *LLMReconciler) reconcileDeployment(ctx context.Context, llm *llmv1alpha1.LLM) error {
	log := logf.FromContext(ctx)

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: llm.Name, Namespace: llm.Namespace},
	}
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, deploy, func() error {
		desired := buildDeployment(llm)
		deploy.Labels = desired.Labels
		deploy.Spec.Replicas = desired.Spec.Replicas
		deploy.Spec.Selector = desired.Spec.Selector
		deploy.Spec.Template = desired.Spec.Template
		return controllerutil.SetControllerReference(llm, deploy, r.Scheme)
	})
	if err != nil {
		return err
	}
	if op != controllerutil.OperationResultNone {
		log.Info("Reconciled Deployment", "operation", op, "name", deploy.Name)
	}
	return nil
}

// reconcileService creates or updates the vLLM Service.
func (r *LLMReconciler) reconcileService(ctx context.Context, llm *llmv1alpha1.LLM) error {
	log := logf.FromContext(ctx)

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: llm.Name, Namespace: llm.Namespace},
	}
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		desired := buildService(llm)
		svc.Labels = desired.Labels
		svc.Spec.Type = desired.Spec.Type
		svc.Spec.Selector = desired.Spec.Selector
		svc.Spec.Ports = desired.Spec.Ports
		return controllerutil.SetControllerReference(llm, svc, r.Scheme)
	})
	if err != nil {
		return err
	}
	if op != controllerutil.OperationResultNone {
		log.Info("Reconciled Service", "operation", op, "name", svc.Name)
	}
	return nil
}

// updateStatus refreshes the LLM status from the backing Deployment.
func (r *LLMReconciler) updateStatus(ctx context.Context, llm *llmv1alpha1.LLM) error {
	var deploy appsv1.Deployment
	if err := r.Get(ctx, client.ObjectKeyFromObject(llm), &deploy); err != nil {
		return err
	}

	llm.Status.Replicas = deploy.Status.Replicas
	llm.Status.AvailableReplicas = deploy.Status.AvailableReplicas
	llm.Status.Endpoint = endpointFor(llm)
	llm.Status.ObservedGeneration = llm.Generation

	ready := deploy.Status.AvailableReplicas > 0
	if ready {
		llm.Status.Phase = llmv1alpha1.LLMPhaseReady
		apimeta.SetStatusCondition(&llm.Status.Conditions, metav1.Condition{
			Type:    llmv1alpha1.ConditionAvailable,
			Status:  metav1.ConditionTrue,
			Reason:  "MinimumReplicasAvailable",
			Message: fmt.Sprintf("%d replica(s) available", deploy.Status.AvailableReplicas),
		})
		apimeta.SetStatusCondition(&llm.Status.Conditions, metav1.Condition{
			Type:    llmv1alpha1.ConditionProgressing,
			Status:  metav1.ConditionFalse,
			Reason:  "DeploymentAvailable",
			Message: "vLLM deployment is available",
		})
	} else {
		llm.Status.Phase = llmv1alpha1.LLMPhaseProgressing
		apimeta.SetStatusCondition(&llm.Status.Conditions, metav1.Condition{
			Type:    llmv1alpha1.ConditionAvailable,
			Status:  metav1.ConditionFalse,
			Reason:  "NoReplicasAvailable",
			Message: "Waiting for vLLM replicas to become ready",
		})
		apimeta.SetStatusCondition(&llm.Status.Conditions, metav1.Condition{
			Type:    llmv1alpha1.ConditionProgressing,
			Status:  metav1.ConditionTrue,
			Reason:  "DeploymentProgressing",
			Message: "vLLM deployment is rolling out",
		})
	}

	return r.Status().Update(ctx, llm)
}

// markDegraded records a Degraded condition and surfaces the underlying error so
// the request is requeued.
func (r *LLMReconciler) markDegraded(ctx context.Context, llm *llmv1alpha1.LLM, reason string, cause error) (ctrl.Result, error) {
	llm.Status.Phase = llmv1alpha1.LLMPhaseDegraded
	apimeta.SetStatusCondition(&llm.Status.Conditions, metav1.Condition{
		Type:    llmv1alpha1.ConditionDegraded,
		Status:  metav1.ConditionTrue,
		Reason:  reason,
		Message: cause.Error(),
	})
	if err := r.Status().Update(ctx, llm); err != nil {
		logf.FromContext(ctx).Error(err, "Failed to update Degraded status")
	}
	return ctrl.Result{}, cause
}

// SetupWithManager sets up the controller with the Manager.
func (r *LLMReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&llmv1alpha1.LLM{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Named("llm").
		Complete(r)
}
