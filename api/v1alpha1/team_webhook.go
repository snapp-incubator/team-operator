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
	admissionv1 "k8s.io/api/admission/v1"
	authv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"net/http"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	"strings"
)

// log is for logging in this package.
var teamlog = logf.Log.WithName("team-resource")

const (
	StagingLabel       = "staging"
	ProductionLabel    = "production"
	NameSpaceSkipLabel = "snappcloud.io/pause-team-validation"
)

func (t *Team) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).For(t).Complete()
}

//+kubebuilder:rbac:groups="",resources=namespaces,verbs=create;get;list;patch;update;watch;delete
//+kubebuilder:rbac:groups=authorization.k8s.io,resources=localsubjectaccessreviews,verbs=create
//+kubebuilder:webhook:path=/validate-team-snappcloud-io-v1alpha1-team,mutating=false,failurePolicy=fail,sideEffects=None,groups=team.snappcloud.io,resources=teams,verbs=create;update,versions=v1alpha1,name=vteam.kb.io,admissionReviewVersions=v1

func ValidateCreate(obj *Team, currentUser string) error {
	var teamNS corev1.Namespace
	teamlog.Info("validating team create", "name", obj.GetName())
	clientSet, err := GetClientSet()
	if err != nil {
		teamlog.Error(err, "error happened while validating create", "namespace", obj.GetNamespace(), "name", obj.GetName())
		return errors.New("could not create client, failed to update team object")
	}
	for _, ns := range obj.Spec.Projects {
		// Check if namespace has the label to be skipped by controller/webhook
		shouldSkip, errSkip := nsSkips(clientSet, ns.Name)
		if errSkip != nil {
			return errSkip
		} else if shouldSkip {
			continue
		}

		// Check if namespace does not exist or has been deleted
		teamNS, err = nsExists(clientSet, obj.Name, ns.Name)
		if err != nil {
			return err
		}

		// Check if namespace already has been added to another team
		err = nsHasTeam(obj, &teamNS)
		if err != nil {
			return err
		}

		// Check If user has access to this namespace
		err = teamAdminAccess(obj, clientSet, ns.Name, currentUser)
		if err != nil {
			return err
		}
	}
	return nil
}

func ValidateUpdate(obj *Team, currentUser string) error {
	var teamNS corev1.Namespace
	teamlog.Info("validating team update", "name", obj.GetName())

	clientSet, err := GetClientSet()
	if err != nil {
		teamlog.Error(err, "error happened while validating update", "namespace", obj.GetNamespace(), "name", obj.GetName())
		return errors.New("fail to get client, failed to update team object")
	}
	for _, ns := range obj.Spec.Projects {
		// Check if namespace has the label to be skipped by controller/webhook
		shouldSkip, errSkip := nsSkips(clientSet, ns.Name)
		if errSkip != nil {
			return errSkip
		} else if shouldSkip {
			continue
		}

		//check if namespace does not exist or has been deleted
		teamNS, err = nsExists(clientSet, obj.Name, ns.Name)
		if err != nil {
			return err
		}

		//check to ensure the namespace has a correct label
		if ns.EnvLabel != ProductionLabel && ns.EnvLabel != StagingLabel {
			errMessage := fmt.Sprintf("namespace Label should be \"%s\" or \"%s\", its not a correct label", ProductionLabel, StagingLabel)
			return errors.New(errMessage)
		}

		//check if namespace already has been added to another team
		err = nsHasTeam(obj, &teamNS)
		if err != nil {
			return err
		}

		//Check If user has access to this namespace
		err = teamAdminAccess(obj, clientSet, ns.Name, currentUser)
		if err != nil {
			return err
		}
	}
	return nil
}

func ValidateDelete(obj *Team, currentUser string) error {
	teamlog.Info("validate delete", "name", obj.Name)
	return nil
}

func GetClientSet() (c kubernetes.Clientset, err error) {
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

func nsSkips(c kubernetes.Clientset, ns string) (bool, error) {
	nsToCheck, errNSGet := c.CoreV1().Namespaces().Get(context.TODO(), ns, metav1.GetOptions{})
	if errNSGet != nil {
		return false, errNSGet
	}
	if strings.ToLower(nsToCheck.Labels[NameSpaceSkipLabel]) == "true" {
		return true, nil
	}
	return false, nil
}

func nsExists(c kubernetes.Clientset, team, ns string) (corev1.Namespace, error) {
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

func teamAdminAccess(r *Team, c kubernetes.Clientset, ns, currentUser string) error {
	var currentUserIsAdmin = false
	var allowed = false
	for _, user := range r.Spec.TeamAdmins {
		if user.Name == currentUser {
			currentUserIsAdmin = true
			break
		}
	}

	if currentUserIsAdmin {
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
				User:               currentUser,
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
		}
	} else {
		// check if the user is cluster-admin
		action := authv1.ResourceAttributes{
			Namespace: ns,
			Verb:      "create",
			Resource:  "clusterrole",
			Group:     "rbac.authorization.k8s.io",
			Version:   "v1",
		}
		check := authv1.LocalSubjectAccessReview{
			ObjectMeta: metav1.ObjectMeta{Namespace: ns},
			Spec: authv1.SubjectAccessReviewSpec{
				User:               currentUser,
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
		}
	}

	if !allowed {
		errMessage := fmt.Sprintf("none of the team owners are allowed to add namespace \"%s\" to team \"%s\", please add at least one of team admins as admin of the project with the followig command: oc policy add-role-to-user admin <user> -n %s", ns, r.Name, ns)
		return errors.New(errMessage)
	}
	return nil

}

func NewMutatingWebhook(mgr manager.Manager) (*teamValidator, error) {
	decoder, err := admission.NewDecoder(mgr.GetScheme())
	if err != nil {
		return nil, err
	}
	return &teamValidator{decoder: decoder}, nil
}

type teamValidator struct {
	decoder *admission.Decoder
}

func (a *teamValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	teamObj := &Team{}
	errDecode := a.decoder.Decode(req, teamObj)
	if errDecode != nil {
		fmt.Println("Err Decode:", errDecode)
		return admission.Errored(http.StatusInternalServerError, errDecode)
	}

	if req.Operation == admissionv1.Update {
		errUpdate := ValidateUpdate(teamObj, req.UserInfo.Username)
		if errUpdate != nil {
			return admission.Errored(http.StatusUnprocessableEntity, errUpdate)
		}
		return admission.Allowed("Updated!")
	}

	if req.Operation == admissionv1.Create {
		errCreate := ValidateCreate(teamObj, req.UserInfo.Username)
		if errCreate != nil {
			return admission.Errored(http.StatusUnprocessableEntity, errCreate)
		}
		return admission.Allowed("Created!")
	}

	if req.Operation == admissionv1.Delete {
		errDelete := ValidateDelete(teamObj, req.UserInfo.Username)
		if errDelete != nil {
			return admission.Errored(http.StatusUnprocessableEntity, errDelete)
		}
		return admission.Allowed("Deleted!")
	}

	return admission.Allowed("done")
}

func (a *teamValidator) Default(ctx context.Context, obj runtime.Object) error {
	return nil
}
