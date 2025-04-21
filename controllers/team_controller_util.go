package controllers

import (
	"context"
	teamv1alpha1 "github.com/snapp-incubator/team-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func (t *TeamReconciler) AddTeamObjectFinalizer(ctx context.Context, team *teamv1alpha1.Team) error {
	if !controllerutil.ContainsFinalizer(team, TeamObjectFinalizer) {
		controllerutil.AddFinalizer(team, TeamObjectFinalizer)
		err := t.Client.Update(ctx, team)
		if err != nil {
			return err
		}
	}
	return nil
}

func (t *TeamReconciler) DeleteTeamObjectFinalizer(ctx context.Context, team *teamv1alpha1.Team) error {
	if controllerutil.ContainsFinalizer(team, TeamObjectFinalizer) {
		controllerutil.RemoveFinalizer(team, TeamObjectFinalizer)
		if errNSFinalizerDelete := t.Client.Update(ctx, team); errNSFinalizerDelete != nil {
			return errNSFinalizerDelete
		}
	}
	return nil
}

func (t *TeamReconciler) CreateTeamMetricNS(ctx context.Context, req ctrl.Request) error {
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

func (t *TeamReconciler) DeleteTeamMetricNS(ctx context.Context, req ctrl.Request) error {
	// remove finalizer from Team Metric Namespace

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

func (t *TeamReconciler) AddTeamProjectFinalizer(ctx context.Context, namespace *corev1.Namespace) error {
	if !controllerutil.ContainsFinalizer(namespace, TeamProjectObjectFinalizer) {
		controllerutil.AddFinalizer(namespace, TeamProjectObjectFinalizer)
		if errAddNamespaceFinalizer := t.Update(ctx, namespace); errAddNamespaceFinalizer != nil {
			return errAddNamespaceFinalizer
		}
	}
	return nil
}

func (t *TeamReconciler) DeleteTeamProjectFinalizer(ctx context.Context, namespace *corev1.Namespace, team *teamv1alpha1.Team) error {
	if controllerutil.ContainsFinalizer(namespace, TeamProjectObjectFinalizer) {
		if errFinalize := t.finalizeNamespace(ctx, namespace.Name, team); errFinalize != nil {
			return errFinalize
		}
		controllerutil.RemoveFinalizer(namespace, TeamProjectObjectFinalizer)
		if errUpdateNS := t.Update(ctx, namespace); errUpdateNS != nil {
			return errUpdateNS
		}
	}
	return nil
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
