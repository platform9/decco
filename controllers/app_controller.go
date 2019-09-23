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
	_ "sigs.k8s.io/cluster-api/util/patch"

	deccov1beta3 "github.com/platform9/decco-operator/api/v1beta3"
)

// AppReconciler reconciles a App object
type AppReconciler struct {
	client.Client
	Log logr.Logger
}

// +kubebuilder:rbac:groups=decco.platform9.com,resources=apps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=decco.platform9.com,resources=apps/status,verbs=get;update;patch

func (r *AppReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("app", req.NamespacedName)
	app := &deccov1beta3.App{}
	if err := r.Get(ctx, req.NamespacedName, app); err != nil {
		log.Error(err, "unable to fetch App")
		// we'll ignore not-found errors, since they can't be fixed by an immediate
		// requeue (we'll need to wait for a new notification), and we can get them
		// on deleted requests.
		return ctrl.Result{}, nil
	}
	if app.Spec.VirtualServiceSpec.Hosts[0] != "derp.svc.local" {
		app.Spec.VirtualServiceSpec.Hosts[0] = "derp.svc.local"
	}

	// Initialize the patch helper
	// patchHelper, err := patch.NewHelper(app, r)
	// if err != nil {
	// 	return ctrl.Result{}, err
	// }

	// TODO: Remove this
	if app.Spec.Name == "pf9-qbert" {
		app.Status.Ready = true
	}

	// Ensures we override values of the VirtualServiceSpec that we don't want the App creator to control.
	if err := r.Client.Update(ctx, app); err != nil {
		log.Error(err, "unable to update App status")
		return ctrl.Result{}, err
	}

	if err := r.Status().Update(ctx, app); err != nil {
		log.Error(err, "unable to update App status")
		return ctrl.Result{}, err
	}

	// if err := patchHelper.Patch(ctx, app); err != nil {
	// 	return ctrl.Result{}, err
	// }

	return ctrl.Result{}, nil
}

func (r *AppReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&deccov1beta3.App{}).
		Complete(r)
}
