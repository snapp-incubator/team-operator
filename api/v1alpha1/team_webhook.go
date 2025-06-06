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
	"net/http"
	"strings"
	"sync"

	admissionv1 "k8s.io/api/admission/v1"
	authv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// log is for logging in this package.
var teamlog = logf.Log.WithName("team-resource")

const (
	MetricNamespaceSuffix = "-team"
	StagingLabel          = "staging"
	ProductionLabel       = "production"
	NameSpaceSkipLabel    = "snappcloud.io/pause-team-validation"
	ServiceAccount        = "system:serviceaccount:team-operator-system:team-operator-controller-manager"
)

func (t *Team) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).For(t).Complete()
}

//+kubebuilder:rbac:groups="",resources=namespaces,verbs=create;get;list;patch;update;watch;delete
//+kubebuilder:rbac:groups=authorization.k8s.io,resources=localsubjectaccessreviews,verbs=create
//+kubebuilder:webhook:path=/validate-team-snappcloud-io-v1alpha1-team,mutating=false,failurePolicy=fail,sideEffects=None,groups=team.snappcloud.io,resources=teams,verbs=create;update,versions=v1alpha1,name=vteam.kb.io,admissionReviewVersions=v1

func (t *teamValidator) ValidateCreate(obj *Team, currentUser string) error {
	var teamNS corev1.Namespace
	teamlog.Info("validating team create", "name", obj.GetName())
	clientSet, err := GetClientSet(t.config)
	if err != nil {
		teamlog.Error(err, "error happened while validating create", "namespace", obj.GetNamespace(), "name", obj.GetName())
		return errors.New("could not create client, failed to update team object")
	}
	for _, ns := range obj.Spec.Projects {
		// Check if namespace has the label to be skipped by controller/webhook
		shouldSkip, errSkip := nsSkips(clientSet, ns.Name, obj.Name)
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

func (t *teamValidator) ValidateUpdate(obj *Team, currentUser string) error {
	var teamNS corev1.Namespace
	var wg sync.WaitGroup
	var errChan chan error

	if len(obj.Spec.Projects) > 1 {
		errChan = make(chan error, len(obj.Spec.Projects))
	} else {
		errChan = make(chan error, 1)
	}
	teamlog.Info("validating team update", "name", obj.GetName())

	clientSet, err := GetClientSet(t.config)
	if err != nil {
		teamlog.Error(err, "error happened while validating update", "namespace", obj.GetNamespace(), "name", obj.GetName())
		return errors.New("fail to get client, failed to update team object")
	}

	// seenProjects will prevent duplicate projects
	var seenProjects []Project
	for _, objNS := range obj.Spec.Projects {
		seenNS := false
		for _, objSeenNS := range seenProjects {
			if objNS.Name == objSeenNS.Name {
				seenNS = true
				break
			}
		}
		if !seenNS {
			seenProjects = append(seenProjects, objNS)
		} else {
			return fmt.Errorf("duplicate project %s", objNS.Name)
		}
	}

	for _, ns := range obj.Spec.Projects {
		wg.Add(1)
		go func(ns Project) {
			defer wg.Done()
			// Check if namespace has the label to be skipped by controller/webhook
			shouldSkip, errSkip := nsSkips(clientSet, ns.Name, obj.Name)
			if errSkip != nil {
				errChan <- errSkip
			} else if shouldSkip {
				errChan <- nil
			}

			//check if namespace does not exist or has been deleted
			teamNS, err = nsExists(clientSet, obj.Name, ns.Name)
			if err != nil {
				errChan <- err
			}

			//check to ensure the namespace has a correct label
			if ns.EnvLabel != ProductionLabel && ns.EnvLabel != StagingLabel {
				errMessage := fmt.Sprintf("namespace Label should be \"%s\" or \"%s\", its not a correct label", ProductionLabel, StagingLabel)
				errChan <- errors.New(errMessage)
			}

			//check if namespace already has been added to another team
			err = nsHasTeam(obj, &teamNS)
			if err != nil {
				errChan <- err
			}

			//Check If user has access to this namespace
			err = teamAdminAccess(obj, clientSet, ns.Name, currentUser)
			if err != nil {
				errChan <- err
			}
			errChan <- nil
		}(ns)
	}
	go func() {
		wg.Wait()
		close(errChan)
	}()
	for errThread := range errChan {
		if errThread != nil {
			return errThread
		}
	}
	return nil
}

func (t *teamValidator) ValidateDelete(obj *Team, currentUser string) error {
	teamlog.Info("validate delete", "name", obj.Name)
	return nil
}

func GetClientSet(config *rest.Config) (c kubernetes.Clientset, err error) {
	clientSet, errConf := kubernetes.NewForConfig(config)
	if errConf != nil {
		teamlog.Error(errConf, "can not create clientSet")
		return *clientSet, fmt.Errorf("something went wrong please contact cloud team: %s", errConf.Error())
	}
	return *clientSet, nil
}

func nsSkips(c kubernetes.Clientset, ns, teamName string) (bool, error) {
	if ns == teamName+MetricNamespaceSuffix {
		return true, nil
	}

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
		if val != r.Name && val != "unknown" {
			errorResp := fmt.Sprintf("namespace \"%s\" inside the Namespaces of team \"%s\" already has the team label \"%s\", please ask in cloud-support if you need to detach the namespace from previous team", tns.Name, r.Name, val)
			return errors.New(errorResp)
		}
	}
	return nil
}

func teamAdminAccess(r *Team, c kubernetes.Clientset, ns, currentUser string) error {
	var currentUserIsAdmin = false
	var userIsClusterAdmin = false
	var userIsServiceAccount = false
	var allowed = false

	// check if username is inside the teamAdmins list
	for _, user := range r.Spec.TeamAdmins {
		if user.Name == currentUser {
			currentUserIsAdmin = true
			break
		}
	}

	// check if username is service account
	if currentUser == ServiceAccount {
		userIsServiceAccount = true
	}

	if currentUserIsAdmin || userIsServiceAccount {
		allowed = true
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
			return fmt.Errorf("user %s is not able to modify team %s. error: %v", currentUser, r.Name, errAuth)
		}

		if resp.Status.Allowed {
			allowed = true
			userIsClusterAdmin = true
		}
	}

	if !allowed {
		if currentUserIsAdmin {
			return fmt.Errorf("user %s is teamAdmin but is not allowed to modify team object", currentUser)
		} else if userIsClusterAdmin {
			return fmt.Errorf("user is cluster-admin but an error happened")
		} else {
			return fmt.Errorf("user %s is not allowed to edit team object, please add %s to teamAdmins", currentUser, currentUser)
		}
	}
	return nil

}

func NewMutatingWebhook(mgr manager.Manager) (*teamValidator, error) {
	decoder, err := admission.NewDecoder(mgr.GetScheme())
	if err != nil {
		return nil, err
	}
	return &teamValidator{decoder: decoder, config: mgr.GetConfig()}, nil
}

type teamValidator struct {
	decoder *admission.Decoder
	config  *rest.Config
}

func (t *teamValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	teamObj := &Team{}
	errDecode := t.decoder.Decode(req, teamObj)
	if errDecode != nil {
		fmt.Println("Err Decode:", errDecode)
		return admission.Errored(http.StatusInternalServerError, errDecode)
	}

	if req.Operation == admissionv1.Update {
		errUpdate := t.ValidateUpdate(teamObj, req.UserInfo.Username)
		if errUpdate != nil {
			return admission.Denied(errUpdate.Error())
		}
		return admission.Allowed("Updated!")
	}

	if req.Operation == admissionv1.Create {
		errCreate := t.ValidateCreate(teamObj, req.UserInfo.Username)
		if errCreate != nil {
			return admission.Denied(errCreate.Error())
		}
		return admission.Allowed("Created!")
	}

	if req.Operation == admissionv1.Delete {
		errDelete := t.ValidateDelete(teamObj, req.UserInfo.Username)
		if errDelete != nil {
			return admission.Denied(errDelete.Error())
		}
		return admission.Allowed("Deleted!")
	}

	return admission.Allowed("done")
}

func (a *teamValidator) Default(ctx context.Context, obj runtime.Object) error {
	return nil
}
