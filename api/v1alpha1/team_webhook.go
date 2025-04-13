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

package v1alpha1

import (
	"context"
	"errors"
	"fmt"
	authv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

// log is for logging in this package.
var teamlog = logf.Log.WithName("team-resource")

const (
	MetricNamespaceSuffix = "-team"
	StagingLabel          = "staging"
	ProductionLabel       = "production"
)

func (t *Team) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).For(t).Complete()
}

//+kubebuilder:rbac:groups="",resources=namespaces,verbs=create;get;list;patch;update;watch;delete
//+kubebuilder:rbac:groups=authorization.k8s.io,resources=localsubjectaccessreviews,verbs=create
//+kubebuilder:webhook:path=/validate-team-snappcloud-io-v1alpha1-team,mutating=false,failurePolicy=fail,sideEffects=None,groups=team.snappcloud.io,resources=teams,verbs=create;update,versions=v1alpha1,name=vteam.kb.io,admissionReviewVersions=v1

var _ webhook.Validator = &Team{}
var teamns corev1.Namespace

func (t *Team) ValidateCreate() error {
	teamlog.Info("validating team create", "name", t.GetName())
	clientSet, err := getClient()
	if err != nil {
		teamlog.Error(err, "error happened while validating create", "namespace", t.GetNamespace(), "name", t.GetName())
		return errors.New("could not create client, failed to update team object")
	}
	for _, ns := range t.Spec.Projects {
		// Check if namespace does not exist or has been deleted
		teamns, err = nsExists(clientSet, t.Name, ns.Name)
		if err != nil {
			return err
		}

		// Check if namespace already has been added to another team
		err = nsHasTeam(t, &teamns)
		if err != nil {
			return err
		}

		// Check If user has access to this namespace
		err = teamAdminAccess(t, ns.Name, clientSet)
		if err != nil {
			return err
		}
	}
	return nil
}

func (t *Team) ValidateUpdate(old runtime.Object) error {
	teamlog.Info("validating team update", "name", t.GetName())

	clientSet, err := getClient()
	if err != nil {
		teamlog.Error(err, "error happened while validating update", "namespace", t.GetNamespace(), "name", t.GetName())
		return errors.New("fail to get client, failed to update team object")
	}
	for _, ns := range t.Spec.Projects {
		//check if namespace does not exist or has been deleted
		teamns, err = nsExists(clientSet, t.Name, ns.Name)
		if err != nil {
			return err
		}

		//check to ensure the namespace has a correct label
		if ns.EnvLabel != ProductionLabel && ns.EnvLabel != StagingLabel {
			errMessage := fmt.Sprintf("namespace Label should be \"%s\" or \"%s\", its not a correct label", ProductionLabel, StagingLabel)
			return errors.New(errMessage)
		}

		//check if namespace already has been added to another team
		err = nsHasTeam(t, &teamns)
		if err != nil {
			return err
		}

		//Check If user has access to this namespace
		err = teamAdminAccess(t, ns.Name, clientSet)
		if err != nil {
			return err
		}
	}

	//prevent deleting a namespace that have the team label

	labelSelector := metav1.LabelSelector{MatchLabels: map[string]string{"snappcloud.io/team": t.Name}}

	listOptions := metav1.ListOptions{
		LabelSelector: labels.Set(labelSelector.MatchLabels).String(),
		Limit:         100,
	}
	namespaces, err := clientSet.CoreV1().Namespaces().List(context.TODO(), listOptions)
	if err != nil {
		teamlog.Error(err, "can not get list of namespaces")
	}

	for _, ni := range namespaces.Items {
		exists := false
		for _, ns := range t.Spec.Projects {
			if ni.Name == ns.Name {
				exists = true
			}
		}
		if !exists && ni.Name != t.Name+MetricNamespaceSuffix {
			errMessage := fmt.Sprintf("namespace \"%s\" has team label but does not exist in \"%s\" team", ni.Name, t.Name)
			return errors.New(errMessage)
		}
	}
	return nil
}

func (t *Team) ValidateDelete() error {
	teamlog.Info("validate delete", "name", t.Name)
	return nil
}

func getClient() (c kubernetes.Clientset, err error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		teamlog.Error(err, "can not get in-cluster config.")
		return c, errors.New("something went wrong please contact the cloud team")
	}

	clientSet, err := kubernetes.NewForConfig(config)
	if err != nil {
		teamlog.Error(err, "can not create clientSet")
		return *clientSet, errors.New("something went wrong please contact cloud team")
	}
	return *clientSet, nil
}

func nsExists(c kubernetes.Clientset, team, ns string) (tns corev1.Namespace, err error) {
	teamNS, errNSGet := c.CoreV1().Namespaces().Get(context.TODO(), ns, metav1.GetOptions{})

	if errNSGet != nil {
		errorResp := fmt.Sprintf("Error while getting namespace \"%s\" for team \"%s\". error: %s", ns, team, errNSGet.Error())
		return *teamNS, errors.New(errorResp)
	}
	return *teamNS, nil
}

func nsHasTeam(r *Team, tns *corev1.Namespace) (err error) {
	if val, ok := tns.Labels["snappcloud.io/team"]; ok {
		if tns.Labels["snappcloud.io/team"] != r.Name {
			errorResp := fmt.Sprintf("namespace \"%s\" inside the Namespaces of team \"%s\" already has the team label \"%s\", please ask in cloud-support if you need to detach the namespace from previous team", tns.Name, r.Name, val)
			return errors.New(errorResp)
		}
	}
	return nil
}

func teamAdminAccess(r *Team, ns string, c kubernetes.Clientset) (err error) {
	var allowed = false
	for _, user := range r.Spec.TeamAdmins {
		action := authv1.ResourceAttributes{
			Namespace: ns,
			Verb:      "create",
			Resource:  "rolebindings",
			Group:     "rbac.authorization.k8s.io",
			Version:   "v1",
		}
		check := authv1.LocalSubjectAccessReview{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns},
			Spec: authv1.SubjectAccessReviewSpec{
				User:               user.Name,
				ResourceAttributes: &action,
			},
		}

		resp, errAuth := c.AuthorizationV1().
			LocalSubjectAccessReviews(ns).
			Create(context.TODO(), &check, metav1.CreateOptions{})
		if errAuth != nil {
			teamlog.Error(errAuth, "error happened while checking team owner permission")
			return errAuth
		}

		if resp.Status.Allowed {
			allowed = true
			break
		}
	}

	if !allowed {
		errMessage := fmt.Sprintf("none of the team owners are allowed to add namespace \"%s\" to team \"%s\", please add at least one of team admins as admin of the project with the followig command: oc policy add-role-to-user admin <user> -n %s", ns, r.Name, ns)
		return errors.New(errMessage)
	}
	return nil

}
