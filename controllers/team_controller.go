/*
Copyright 2023.

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
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/rest"

	teamv1alpha1 "github.com/snapp-incubator/team-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	MetricNamespaceSuffix      = "-team"
	TeamObjectFinalizer        = "batch.finalizers.kubebuilder.io/metric-namespace"
	MetaDataLabelDataSource    = "snappcloud.io/datasource"
	MetaDataLabelEnv           = "environment"
	TeamProjectObjectFinalizer = "team.snappcloud.io/cleanup-team"
)

// TeamReconciler reconciles a Team object
type TeamReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Config *rest.Config
}

//+kubebuilder:rbac:groups=team.snappcloud.io,resources=teams,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=team.snappcloud.io,resources=teams/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=team.snappcloud.io,resources=teams/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups="",resources=namespaces/status,verbs=get;update;patch
//+kubebuilder:rbac:groups="",resources=namespaces/finalizers,verbs=update

func (t *TeamReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	loggerObj := log.FromContext(ctx)

	team, errGetTeam, teamDeleted := t.GetTeamObj(ctx, req, loggerObj)
	if errGetTeam != nil {
		loggerObj.Error(errGetTeam, "failed to get team object", "team", team.GetName())
		return ctrl.Result{Requeue: true}, errGetTeam
	} else if teamDeleted {
		return ctrl.Result{}, nil
	}

	errHandleTeamDelete := t.DeleteTeamIfRequired(ctx, req, team, loggerObj)
	if errHandleTeamDelete != nil {
		loggerObj.Error(errHandleTeamDelete, "failed to delete team when deletionTimeStamp is not zero", "team", team.GetName())
		return ctrl.Result{Requeue: true}, errHandleTeamDelete
	}

	errAddTeamFinalizer := t.AddTeamObjectFinalizer(ctx, team)
	if errAddTeamFinalizer != nil {
		loggerObj.Error(errAddTeamFinalizer, "failed to add finalizer to team object", "team", team.GetName())
		return ctrl.Result{Requeue: true}, errAddTeamFinalizer
	}

	metricTeamErr := t.CreateTeamMetricNS(ctx, req)
	if metricTeamErr != nil {
		loggerObj.Error(metricTeamErr, "failed to create metric namespace", "team", team.GetName())
		return ctrl.Result{}, metricTeamErr
	}

	// update Namespaces in Team Projects
	for _, ns := range team.Spec.Projects {
		namespace := &corev1.Namespace{}
		errGetNS := t.Client.Get(ctx, types.NamespacedName{Name: ns.Name}, namespace)
		if errGetNS != nil && apierrors.IsNotFound(errGetNS) {
			loggerObj.Error(errGetNS, "failed to get namespace", "team", team.GetName(), "namespace", ns.Name)
			return ctrl.Result{Requeue: true}, errGetNS
		}

		if namespace.ObjectMeta.DeletionTimestamp.IsZero() {
			errAddProjectFinalizer := t.AddTeamProjectFinalizer(ctx, namespace)
			if errAddProjectFinalizer != nil {
				loggerObj.Error(errAddProjectFinalizer, "Couldn't add the finalizer to namespace", "team", team.GetName(), "namespace", ns.Name, "envLabel", ns.EnvLabel)
				return ctrl.Result{Requeue: true}, errAddProjectFinalizer
			}
		} else {
			errDeleteProjectFinalizer := t.DeleteTeamProjectFinalizer(ctx, namespace, team)
			if errDeleteProjectFinalizer != nil {
				loggerObj.Error(errDeleteProjectFinalizer, "Couldn't delete the finalizer from namespace", "team", team.GetName(), "namespace", ns.Name, "envLabel", ns.EnvLabel)
				return ctrl.Result{Requeue: true}, errDeleteProjectFinalizer
			}

			// Stop reconciliation as the item is being deleted
			return ctrl.Result{Requeue: true}, nil
		}

		currentEnv := namespace.Labels[MetaDataLabelEnv]
		loggerObj.Info(fmt.Sprintf("Updating Team: %s Namespace: %s CurrentEnv: %v DesiredEnv: %s", team.GetName(), ns.Name, currentEnv, ns.EnvLabel))
		namespace.Labels["snappcloud.io/team"] = team.GetName()
		namespace.Labels[MetaDataLabelEnv] = ns.EnvLabel
		if ns.EnvLabel == teamv1alpha1.ProductionLabel {
			namespace.Labels[MetaDataLabelDataSource] = "true"
		}

		errUpdate := t.Client.Update(ctx, namespace)
		if errUpdate != nil {
			loggerObj.Error(errUpdate, "failed to update namespace", "namespace", ns)
			return ctrl.Result{}, errUpdate
		}
	}

	// add all namespaces with the current team label to the team object
	labelSelector := metav1.LabelSelector{MatchLabels: map[string]string{"snappcloud.io/team": team.Name}}
	listOptions := metav1.ListOptions{
		LabelSelector: labels.Set(labelSelector.MatchLabels).String(),
		Limit:         100,
	}

	clientSet, getClientSetErr := teamv1alpha1.GetClientSet(t.Config)
	if getClientSetErr != nil {
		return ctrl.Result{}, getClientSetErr
	}
	namespaces, err := clientSet.CoreV1().Namespaces().List(context.TODO(), listOptions)
	if err != nil {
		loggerObj.Error(err, "can not get list of namespaces")
		return ctrl.Result{}, err
	}

	var namespacesToBeAdded []teamv1alpha1.Project
	var exists bool
	for _, ni := range namespaces.Items {
		if ni.Name == team.Name+MetricNamespaceSuffix {
			continue
		}
		exists = false
		for _, ns := range team.Spec.Projects {
			if ni.Name == ns.Name {
				exists = true
				break
			}
		}
		if !exists {
			envLabel := ni.Labels[MetaDataLabelEnv]
			if len(envLabel) == 0 {
				envLabel = teamv1alpha1.StagingLabel
			}
			namespacesToBeAdded = append(namespacesToBeAdded, teamv1alpha1.Project{Name: ni.Name, EnvLabel: envLabel})
		}
	}

	team.Spec.Projects = append(team.Spec.Projects, namespacesToBeAdded...)
	errUpdateTeam := t.Client.Update(ctx, team)
	if errUpdateTeam != nil {
		return ctrl.Result{}, errUpdateTeam
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (t *TeamReconciler) SetupWithManager(mgr ctrl.Manager) error {
	labelPredicate := predicate.NewPredicateFuncs(func(obj client.Object) bool {
		labelsMap := obj.GetLabels()
		_, exists := labelsMap["snappcloud.io/team"]
		return exists
	})

	mapFunc := func(a client.Object) []reconcile.Request {
		ctx := context.Background()
		loggerObj := log.FromContext(ctx)

		var requests []reconcile.Request

		var teamList teamv1alpha1.TeamList
		if err := mgr.GetClient().List(ctx, &teamList, &client.ListOptions{}); err != nil {
			loggerObj.Error(err, "Unable to list team resources")
			return nil
		}

		for _, team := range teamList.Items {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      team.Name,
					Namespace: team.Namespace,
				},
			})
		}

		return requests
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&teamv1alpha1.Team{}).
		Watches(
			&source.Kind{Type: &corev1.Namespace{}},
			handler.EnqueueRequestsFromMapFunc(mapFunc),
			builder.WithPredicates(labelPredicate),
		).
		Complete(t)
}

// GetTeamObj returns the team object, error and a bool that indicates if team has been removed
func (t *TeamReconciler) GetTeamObj(ctx context.Context, req ctrl.Request, loggerObj logr.Logger) (*teamv1alpha1.Team, error, bool) {
	team := &teamv1alpha1.Team{}
	errGetTeam := t.Client.Get(ctx, req.NamespacedName, team)
	if errGetTeam != nil {
		// check if team is already deleted or not
		if apierrors.IsNotFound(errGetTeam) {
			loggerObj.Info("team resource not found. Ignoring since the object must be deleted", "team", req.NamespacedName)
			// remove the Metric Namespace when the team object is removed
			errDeleteMetricNS := t.DeleteTeamMetricNS(ctx, req)
			if errDeleteMetricNS != nil {
				loggerObj.Error(errDeleteMetricNS, "couldn't delete metric namespace", "team", team.GetName())
				return nil, errDeleteMetricNS, false
			}
			return nil, nil, true
		} else {
			loggerObj.Error(errGetTeam, "Failed to get team", "team", team.GetName())
			return nil, errGetTeam, false
		}
	}
	return team, nil, false
}

func (t *TeamReconciler) DeleteTeamIfRequired(ctx context.Context, req ctrl.Request, team *teamv1alpha1.Team, loggerObj logr.Logger) error {
	// check if controller should delete the team
	if !team.ObjectMeta.GetDeletionTimestamp().IsZero() {
		// TODO: Remove team.Spec.Projects finalizers

		// remove the Metric Namespace
		errNSDeleted := t.DeleteTeamMetricNS(ctx, req)
		if errNSDeleted != nil && !errors.IsNotFound(errNSDeleted) {
			return errNSDeleted
		}

		// remove the team finalizer
		errNSDeleteFinalizer := t.DeleteTeamObjectFinalizer(ctx, team)
		if errNSDeleteFinalizer != nil {
			return errNSDeleteFinalizer
		}
		return nil
	}
	return nil
}
