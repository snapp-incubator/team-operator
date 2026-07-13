package controllers

import (
	"context"
	"fmt"
	"strings"

	teamv1alpha1 "github.com/snapp-incubator/team-operator/api/v1alpha1"
	"github.com/snapp-incubator/team-operator/internal/iam"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

func rbacConditionNeedsUpdate(conditions []metav1.Condition, desired metav1.Condition) bool {
	existing := apimeta.FindStatusCondition(conditions, desired.Type)
	if existing == nil {
		return true
	}
	return existing.Status != desired.Status ||
		existing.Reason != desired.Reason ||
		existing.Message != desired.Message ||
		existing.ObservedGeneration != desired.ObservedGeneration
}

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
			if !apierrors.IsAlreadyExists(errCreate) {
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
			if !apierrors.IsAlreadyExists(errCreate) {
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
		if !apierrors.IsNotFound(err) {
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

func (t *TeamReconciler) ensureTeamAdminRBAC(ctx context.Context, team *teamv1alpha1.Team) error {
	roleName := team.Name + "-team-clusterrole"
	bindingName := team.Name + "-team-clusterrolebinding"
	managedLabels := map[string]string{
		"app.kubernetes.io/managed-by": "team-operator",
		"team.snappcloud.io/team":      team.Name,
	}

	cr := &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: roleName}}
	if _, err := controllerutil.CreateOrUpdate(ctx, t.Client, cr, func() error {
		cr.Labels = managedLabels
		cr.Rules = []rbacv1.PolicyRule{{
			APIGroups:     []string{"team.snappcloud.io"},
			Resources:     []string{"teams"},
			ResourceNames: []string{team.Name},
			Verbs:         []string{"get", "patch", "update"},
		}}
		return ctrl.SetControllerReference(team, cr, t.Scheme)
	}); err != nil {
		return err
	}

	var adminNames []string
	if t.EnableIAMTeamAdminAccess {
		iamCtx, cancel := context.WithTimeout(ctx, t.IAMTeamAPITimeout)
		defer cancel()
		names, err := iam.FetchTeamAdmins(iamCtx, nil, t.IAMTeamAPIURL, team.Name)
		if err != nil {
			return fmt.Errorf("failed to fetch team admins from IAM, will retry: %w", err)
		}
		adminNames = names
	} else {
		for _, a := range team.Spec.TeamAdmins {
			adminNames = append(adminNames, a.Name)
		}
	}

	subjects := make([]rbacv1.Subject, 0, len(adminNames))
	for _, name := range adminNames {
		if strings.HasPrefix(name, "system:serviceaccount:") {
			rest := strings.TrimPrefix(name, "system:serviceaccount:")
			parts := strings.SplitN(rest, ":", 2)
			if len(parts) == 2 {
				subjects = append(subjects, rbacv1.Subject{
					Kind:      "ServiceAccount",
					Namespace: parts[0],
					Name:      parts[1],
				})
			} else {
				log.FromContext(ctx).Info("skipping malformed serviceaccount admin name, expected format system:serviceaccount:<namespace>:<name>", "team", team.Name, "admin", name)
			}
		} else {
			subjects = append(subjects, rbacv1.Subject{
				Kind:     "User",
				APIGroup: "rbac.authorization.k8s.io",
				Name:     name,
			})
		}
	}

	crb := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: bindingName}}
	if _, err := controllerutil.CreateOrUpdate(ctx, t.Client, crb, func() error {
		crb.Labels = managedLabels
		crb.RoleRef = rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     roleName,
		}
		crb.Subjects = subjects
		return ctrl.SetControllerReference(team, crb, t.Scheme)
	}); err != nil {
		return err
	}

	return nil
}
