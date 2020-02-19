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
	"fmt"

	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	deccov1 "github.com/platform9/decco/api/v1beta2"
)

// SpaceReconciler reconciles a Space object
type SpaceReconciler struct {
	client.Client
	Log logr.Logger
}

// +kubebuilder:rbac:groups=decco.platform9.com,resources=spaces,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=decco.platform9.com,resources=spaces/status,verbs=get;update;patch

func (r *SpaceReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("space", req.NamespacedName)

	// Lookup the current keys
	space := deccov1.Space{}
	err := r.Client.Get(ctx, req.NamespacedName, &space)
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Check if object is being deleted
	if !space.ObjectMeta.DeletionTimestamp.IsZero() {
		log.Info("Ignoring object being deleted.")
		return ctrl.Result{}, nil
	}

	// Validate object
	err = space.Spec.Validate()
	if err != nil {
		return ctrl.Result{}, err
	}

	// Decide what to do based on current phase
	switch space.Status.Phase {
	case deccov1.SpacePhaseNone:
		shouldCreateResources = true
	case deccov1.SpacePhaseCreating:
	case deccov1.SpacePhaseActive:
		// Nothing to do when active
	default:
		return ctrl.Result{}, fmt.Errorf("unexpected space phase: %s", space.Status.Phase)
	}

	return ctrl.Result{}, nil
}

func (r *SpaceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&deccov1.Space{}).
		Complete(r)
}
