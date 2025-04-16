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
	"k8s.io/apimachinery/pkg/labels"

	teamv1alpha1 "github.com/snapp-incubator/team-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	MetricNamespaceSuffix    = "-team"
	MetricNamespaceFinalizer = "batch.finalizers.kubebuilder.io/metric-namespace"
	MetaDataLabelDataSource  = "snappcloud.io/datasource"
	MetaDataLabelEnv         = "environment"
	teamFinalizer            = "team.snappcloud.io/cleanup-team"
)

// TeamReconciler reconciles a Team object
type TeamReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=team.snappcloud.io,resources=teams,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=team.snappcloud.io,resources=teams/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=team.snappcloud.io,resources=teams/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups="",resources=namespaces/status,verbs=get;update;patch
//+kubebuilder:rbac:groups="",resources=namespaces/finalizers,verbs=update

func (t *TeamReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	loggerObj := log.FromContext(ctx)

	team := &teamv1alpha1.Team{}

	errClient := t.Client.Get(ctx, req.NamespacedName, team)
	if errClient != nil {
		if apierrors.IsNotFound(errClient) {
			loggerObj.Info("team resource not found. Ignoring since the object must be deleted", "team", req.NamespacedName)
			errClient = t.checkMetricNSForTeamIsDeleted(ctx, req)
			if errClient != nil {
				return ctrl.Result{}, errClient
			}
			return ctrl.Result{}, nil
		}
		loggerObj.Error(errClient, "Failed to get team", "team", req.NamespacedName)
		return ctrl.Result{}, errClient
	}

	if !team.ObjectMeta.GetDeletionTimestamp().IsZero() {
		errNSDeleted := t.checkMetricNSForTeamIsDeleted(ctx, req)
		if errNSDeleted != nil && !errors.IsNotFound(errNSDeleted) {
			return ctrl.Result{}, errNSDeleted
		}
		if controllerutil.ContainsFinalizer(team, MetricNamespaceFinalizer) {
			controllerutil.RemoveFinalizer(team, MetricNamespaceFinalizer)
			if errNSDeleted = t.Client.Update(ctx, team); errNSDeleted != nil {
				return ctrl.Result{}, errNSDeleted
			}
		}
		return ctrl.Result{}, nil
	}

	errAddFinalizer := t.CheckMetricNSFinalizerIsAdded(ctx, team)
	if errAddFinalizer != nil {
		return ctrl.Result{}, errAddFinalizer
	}

	metricTeamErr := t.CheckMetricNSForTeamIsCreated(ctx, req)
	if metricTeamErr != nil {
		return ctrl.Result{}, metricTeamErr
	}

	adminAccessToMetricNsErr := t.CheckMetricNSAdminAccessToTeamOwners(ctx, req, team)
	if adminAccessToMetricNsErr != nil {
		return ctrl.Result{}, adminAccessToMetricNsErr
	}

	updateAdminsErr := t.UpdateAdminsBackwardCompatibility(ctx, team)
	if updateAdminsErr != nil {
		return ctrl.Result{}, updateAdminsErr
	}

	// adding team label for each namespace in team spec
	for _, ns := range team.Spec.Projects {
		namespace := &corev1.Namespace{}

		err := t.Client.Get(ctx, types.NamespacedName{Name: ns.Name}, namespace)
		if err != nil {
			loggerObj.Error(err, "failed to get namespace", "namespace", ns.Name)
			return ctrl.Result{}, err
		}
		namespace.Labels["snappcloud.io/team"] = team.GetName()
		namespace.Labels[MetaDataLabelEnv] = ns.EnvLabel
		if ns.EnvLabel == teamv1alpha1.ProductionLabel {
			namespace.Labels[MetaDataLabelDataSource] = "true"
		}

		if namespace.ObjectMeta.DeletionTimestamp.IsZero() {
			if !controllerutil.ContainsFinalizer(namespace, teamFinalizer) {
				controllerutil.AddFinalizer(namespace, teamFinalizer)
				if err := t.Update(ctx, namespace); err != nil {
					return ctrl.Result{}, err
				}
			}
		} else {
			if controllerutil.ContainsFinalizer(namespace, teamFinalizer) {
				if errFinalize := t.finalizeNamespace(ctx, namespace.Name, team); errFinalize != nil {
					loggerObj.Error(errFinalize, "failed to finalize namespace", "namespace", ns.Name, "team", team.Name)
					return ctrl.Result{}, errFinalize
				}
				controllerutil.RemoveFinalizer(namespace, teamFinalizer)
				if err := t.Update(ctx, namespace); err != nil {
					return ctrl.Result{}, err
				}
			}

			// Stop reconciliation as the item is being deleted
			return ctrl.Result{}, nil
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

	clientSet, getClientSetErr := teamv1alpha1.GetClientSet()
	if getClientSetErr != nil {
		return ctrl.Result{}, getClientSetErr
	}
	namespaces, err := clientSet.CoreV1().Namespaces().List(context.TODO(), listOptions)
	if err != nil {
		loggerObj.Error(err, "can not get list of namespaces")
		return ctrl.Result{}, err
	}

	var namespacesToBeAdded []teamv1alpha1.Project
	for _, ni := range namespaces.Items {
		exists := false
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

func (t *TeamReconciler) UpdateAdminsBackwardCompatibility(ctx context.Context, team *teamv1alpha1.Team) error {
	var oldAdminExists = false
	for _, user := range team.Spec.TeamAdmins {
		if user.Name == team.Spec.TeamAdmin {
			oldAdminExists = true
			break
		}
	}

	if !oldAdminExists {
		team.Spec.TeamAdmins = append(team.Spec.TeamAdmins, teamv1alpha1.Admin{Name: team.Spec.TeamAdmin})
		err := t.Client.Update(ctx, team)
		if err != nil {
			return err
		}
	}
	return nil
}

func (t *TeamReconciler) CheckMetricNSFinalizerIsAdded(ctx context.Context, team *teamv1alpha1.Team) error {
	if !controllerutil.ContainsFinalizer(team, MetricNamespaceFinalizer) {
		controllerutil.AddFinalizer(team, MetricNamespaceFinalizer)
		err := t.Client.Update(ctx, team)
		if err != nil {
			return err
		}
	}
	return nil
}

func (t *TeamReconciler) CheckMetricNSForTeamIsCreated(ctx context.Context, req ctrl.Request) error {
	desiredName := req.Name + MetricNamespaceSuffix
	metricTeamNS := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: desiredName,
			Labels: map[string]string{
				"snappcloud.io/team": req.Name,
			},
		},
	}

	nsTmp := &corev1.Namespace{}
	errGet := t.Client.Get(ctx, types.NamespacedName{Name: desiredName}, nsTmp)
	if errGet != nil {
		errCreate := t.Client.Create(ctx, metricTeamNS)
		if errCreate != nil {
			if !errors.IsAlreadyExists(errCreate) {
				return errCreate
			}
		}
	}

	var hasTeam = false
	for key, value := range nsTmp.ObjectMeta.Labels {
		if key == "snappcloud.io/team" {
			if value == req.Name {
				hasTeam = true
				break
			}
		}
	}
	if !hasTeam {
		errCreate := t.Client.Update(ctx, metricTeamNS)
		if errCreate != nil {
			if !errors.IsAlreadyExists(errCreate) {
				return errCreate
			}
		}
	}
	return nil
}

func (t *TeamReconciler) CheckMetricNSAdminAccessToTeamOwners(ctx context.Context, req ctrl.Request, team *teamv1alpha1.Team) error {
	roleBindingName := "admin-access-team-operator"
	namespaceName := team.Name + MetricNamespaceSuffix

	var subjects []rbacv1.Subject
	for _, admin := range team.Spec.TeamAdmins {
		subjects = append(subjects, rbacv1.Subject{
			Kind:     rbacv1.UserKind,
			Name:     admin.Name,
			APIGroup: rbacv1.GroupName,
		})
	}
	roleBindingDesired := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      roleBindingName,
			Namespace: namespaceName,
		},
		Subjects: subjects,
		RoleRef: rbacv1.RoleRef{
			Kind:     "ClusterRole",
			Name:     "admin",
			APIGroup: rbacv1.GroupName,
		},
	}

	roleBinding := &rbacv1.RoleBinding{}
	err := t.Get(ctx, client.ObjectKey{
		Namespace: namespaceName,
		Name:      roleBindingName,
	}, roleBinding)

	if err != nil && errors.IsNotFound(err) {
		// Doesn't exist, so create it
		if errCreate := t.Client.Create(ctx, roleBindingDesired); err != nil {
			return errCreate
		}
	} else if err != nil {
		return err
	}

	// exists so we check for updates
	errUpdate := t.Client.Update(ctx, roleBindingDesired)
	if errUpdate != nil {
		return errUpdate
	}
	return nil
}

func (t *TeamReconciler) checkMetricNSForTeamIsDeleted(ctx context.Context, req ctrl.Request) error {
	metricTeamNS := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: req.Name + MetricNamespaceSuffix,
		},
	}
	err := t.Client.Delete(ctx, metricTeamNS)
	if err != nil {
		if !errors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (t *TeamReconciler) SetupWithManager(mgr ctrl.Manager) error {

	labelPredicate := predicate.NewPredicateFuncs(func(obj client.Object) bool {
		labels := obj.GetLabels()
		_, exists := labels["snappcloud.io/team"]
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

func (t *TeamReconciler) finalizeNamespace(ctx context.Context, deletedNamespace string, team *teamv1alpha1.Team) error {
	var desiredProjects []teamv1alpha1.Project
	for _, namespace := range team.Spec.Projects {
		if namespace.Name != deletedNamespace {
			desiredProjects = append(desiredProjects, namespace)
		}
	}

	team.Spec.Projects = desiredProjects

	if err := t.Client.Update(ctx, team); err != nil {
		return err
	}

	return nil
}
