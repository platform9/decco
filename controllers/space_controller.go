/*

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

package controllers

import (
	"context"

	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	// Imports lifted from cluster-api
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	"sigs.k8s.io/cluster-api/util/patch"

	deccov1beta3 "github.com/platform9/decco/api/v1beta3"
)

// SpaceReconciler reconciles a Space object
type SpaceReconciler struct {
	client.Client
	Log logr.Logger
}

// +kubebuilder:rbac:groups=decco.platform9.com,resources=spaces,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=decco.platform9.com,resources=spaces/status,verbs=get;update;patch

// Reconcile handles all Space events
func (r *SpaceReconciler) Reconcile(req ctrl.Request) (_ ctrl.Result, reconcileError error) {
	// func (r *SpaceReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("space", req.NamespacedName)

	s := &deccov1beta3.Space{}

	if err := r.Get(ctx, req.NamespacedName, s); err != nil {
		if apierrors.IsNotFound(err) {
			// Object not found, return.  Created objects are automatically garbage collected.
			// For additional cleanup logic use finalizers.
			return ctrl.Result{}, nil
		}

		// Error reading the object - requeue the request.
		return ctrl.Result{}, err
	}

	// Initialize the patch helper
	patchHelper, err := patch.NewHelper(s, r)
	if err != nil {
		return ctrl.Result{}, err
	}

	defer func() {
		log.V(1).Info("Starting reconciliation of phase")
		// Always reconcile the Status.Phase field.
		r.reconcilePhase(ctx, s)
		// Always attempt to Patch the Machine object and status after each reconciliation.
		if err := patchHelper.Patch(ctx, s); err != nil {
			if reconcileError == nil {
				reconcileError = err
			}
		}
	}()

	return r.reconcile(ctx, s)
}

// reconcile is an "inner" reconcile loop. Additional args can be added to this signature.
func (r *SpaceReconciler) reconcile(ctx context.Context, s *deccov1beta3.Space) (ctrl.Result, error) {
	// Call the inner reconciliation methods.
	// This will be good if we need to implement internal reconcile functions for different Space aspects
	/*
		reconciliationErrors := []error{
			r.reconcileBootstrap(ctx, m),
			r.reconcileInfrastructure(ctx, m),
			r.reconcileNodeRef(ctx, cluster, m),
			r.reconcileClusterStatus(ctx, cluster, m),
		}

		// Parse the errors, making sure we record if there is a RequeueAfterError.
		res := ctrl.Result{}
		errs := []error{}
		for _, err := range reconciliationErrors {
			if requeueErr, ok := errors.Cause(err).(capierrors.HasRequeueAfterError); ok {
				// Only record and log the first RequeueAfterError.
				if !res.Requeue {
					res.Requeue = true
					res.RequeueAfter = requeueErr.GetRequeueAfter()
					klog.Infof("Reconciliation for Machine %q in namespace %q asked to requeue: %v", m.Name, m.Namespace, err)
				}
				continue
			}

			errs = append(errs, err)
		}
		return res, kerrors.NewAggregate(errs)
	*/
	errs := []error{}
	return ctrl.Result{}, kerrors.NewAggregate(errs)

}

func (r *SpaceReconciler) reconcilePhase(ctx context.Context, s *deccov1beta3.Space) {
	// Set the phase to "pending" if nil.
	if s.Status.Phase == "" {
		s.Status.SetTypedPhase(deccov1beta3.SpacePhasePending)
	}

	if s.Spec.ManualPhase == "active" {
		s.Status.SetTypedPhase(deccov1beta3.SpacePhaseActive)
	}

	if s.Spec.ManualPhase == "deploying" {
		s.Status.SetTypedPhase(deccov1beta3.SpacePhaseDeploying)
	}

	/* These comments from cluster-api are here for reference

	// Set the phase to "provisioning" if bootstrap is ready and the infrastructure isn't.
	if m.Status.BootstrapReady && !m.Status.InfrastructureReady {
		m.Status.SetTypedPhase(clusterv1.MachinePhaseProvisioning)
	}

	// Set the phase to "provisioned" if the infrastructure is ready.
	if m.Status.InfrastructureReady {
		m.Status.SetTypedPhase(clusterv1.MachinePhaseProvisioned)
	}

	// Set the phase to "running" if there is a NodeRef field.
	if m.Status.NodeRef != nil && m.Status.InfrastructureReady {
		m.Status.SetTypedPhase(clusterv1.MachinePhaseRunning)
	}

	// Set the phase to "failed" if any of Status.ErrorReason or Status.ErrorMessage is not-nil.
	if m.Status.ErrorReason != nil || m.Status.ErrorMessage != nil {
		m.Status.SetTypedPhase(clusterv1.MachinePhaseFailed)
	}

	// Set the phase to "deleting" if the deletion timestamp is set.
	if !m.DeletionTimestamp.IsZero() {
		m.Status.SetTypedPhase(clusterv1.MachinePhaseDeleting)
	}
	*/
}

func (r *SpaceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&deccov1beta3.Space{}).
		Complete(r)
}
