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

func (r *Team) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

// TODO(user): EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!

// TODO(user): change verbs to "verbs=create;update;delete" if you want to enable deletion validation.
//+kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;patch;update;watch
//+kubebuilder:rbac:groups=authorization.k8s.io,resources=localsubjectaccessreviews,verbs=create
//+kubebuilder:webhook:path=/validate-team-snappcloud-io-v1alpha1-team,mutating=false,failurePolicy=fail,sideEffects=None,groups=team.snappcloud.io,resources=teams,verbs=create;update,versions=v1alpha1,name=vteam.kb.io,admissionReviewVersions=v1

var _ webhook.Validator = &Team{}
var teamns corev1.Namespace

func (r *Team) ValidateCreate() error {
	teamlog.Info("validate create", "name", r.Name)
	clientset, err := getClinet()

	for _, ns := range r.Spec.Namespaces {

		//check if namespace does not exist or has been deleted
		teamns, err = nsExists(ns, clientset)
		if err != nil {
			return err
		}

		//check if namespace already has been added to another team
		err = nsHasTeam(r, &teamns)
		if err != nil {
			return err
		}

		//Check If user has access to this namespace
		err = teamAdminAccess(r, ns, clientset)
		if err != nil {
			return err
		}

	}
	return nil
}

func (r *Team) ValidateUpdate(old runtime.Object) error {
	teamlog.Info("validate update", "name", r.Name)

	clientset, err := getClinet()

	for _, ns := range r.Spec.Namespaces {

		//check if namespace does not exist or has been deleted
		teamns, err = nsExists(ns, clientset)
		if err != nil {
			return err
		}

		//check if namespace already has been added to another team
		err = nsHasTeam(r, &teamns)
		if err != nil {
			return err
		}

		//Check If user has access to this namespace
		err = teamAdminAccess(r, ns, clientset)
		if err != nil {
			return err
		}
	}

	//prevent deleting a namespace that have the teamlabel

	labelSelector := metav1.LabelSelector{MatchLabels: map[string]string{"snappcloud.io/team": r.Name}}

	listOptions := metav1.ListOptions{
		LabelSelector: labels.Set(labelSelector.MatchLabels).String(),
		Limit:         100,
	}
	namespaces, err := clientset.CoreV1().Namespaces().List(context.TODO(), listOptions)
	if err != nil {
		teamlog.Error(err, "can not get namesapces")
	}

	for _, ni := range namespaces.Items {
		exists := false
		for _, ns := range r.Spec.Namespaces {
			if ni.Name == ns {
				exists = true
			}
		}
		if exists == false {
			return errors.New("you can not remove the namespaces which have team label.")
		}

	}
	return nil
}

func (r *Team) ValidateDelete() error {
	teamlog.Info("validate delete", "name", r.Name)
	return nil
}

func getClinet() (c kubernetes.Clientset, err error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		teamlog.Error(err, "can not get incluster config.")
		return c, errors.New("something is wrong please contact the cloud team.")

	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		teamlog.Error(err, "can not create clientset")
		return *clientset, errors.New("something is wrong please contact cloud team")

	}
	return *clientset, nil

}

func nsExists(ns string, c kubernetes.Clientset) (tns corev1.Namespace, err error) {
	teamns, err := c.CoreV1().Namespaces().Get(context.TODO(), ns, metav1.GetOptions{})

	if err != nil {
		errorresp := "namespace " + ns + " does not exist or has been deleted."
		return *teamns, errors.New(errorresp)
	}
	return *teamns, nil
}

func nsHasTeam(r *Team, tns *corev1.Namespace) (err error) {
	if val, ok := tns.Labels["snappcloud.io/team"]; ok {
		if tns.Labels["snappcloud.io/team"] != r.Name {
			errorresp := "namespace " + tns.Name + " already have the team label " + val
			return errors.New(errorresp)
		}

	}
	return nil

}

func teamAdminAccess(r *Team, ns string, c kubernetes.Clientset) (err error) {
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
			User:               r.Spec.TeamAdmin,
			ResourceAttributes: &action,
		},
	}

	resp, err := c.AuthorizationV1().
		LocalSubjectAccessReviews(ns).
		Create(context.TODO(), &check, metav1.CreateOptions{})

	if err != nil {
		teamlog.Error(err, "can not create role binding.")
	}
	if !resp.Status.Allowed {
		return errors.New("you are not allowed to add namespace. " + ns)
	}
	return nil

}
