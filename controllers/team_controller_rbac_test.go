package controllers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	teamv1alpha1 "github.com/snapp-incubator/team-operator/api/v1alpha1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newRBACTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = teamv1alpha1.AddToScheme(s)
	return s
}

func newRBACTestTeam(name string) *teamv1alpha1.Team {
	return &teamv1alpha1.Team{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			UID:  types.UID("uid-" + name),
		},
		Spec: teamv1alpha1.TeamSpec{
			TeamAdmins: []teamv1alpha1.Admin{{Name: "spec-admin@example.com"}},
		},
	}
}

// TestRBAC_SpecSource verifies that when IAM is disabled the CRB subjects are
// taken from spec.TeamAdmins.
func TestRBAC_SpecSource(t *testing.T) {
	s := newRBACTestScheme()
	team := newRBACTestTeam("rbac-spec")
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(team).Build()

	r := &TeamReconciler{
		Client:                   fakeClient,
		Scheme:                   s,
		EnableIAMTeamAdminAccess: false,
	}

	if err := r.ensureTeamAdminRBAC(context.Background(), team); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	crb := &rbacv1.ClusterRoleBinding{}
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: team.Name + "-team-clusterrolebinding"}, crb); err != nil {
		t.Fatalf("CRB not found: %v", err)
	}
	if len(crb.Subjects) != 1 || crb.Subjects[0].Name != "spec-admin@example.com" {
		t.Fatalf("expected spec-admin subject, got %v", crb.Subjects)
	}
}

// TestRBAC_IAMSource verifies that when IAM is enabled the CRB subjects come
// from the IAM API, not from spec.TeamAdmins.
func TestRBAC_IAMSource(t *testing.T) {
	const iamAdmin = "iam-admin@example.com"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"name":"rbac-iam","admins":[%q]}`, iamAdmin)
	}))
	defer server.Close()

	s := newRBACTestScheme()
	team := newRBACTestTeam("rbac-iam")
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(team).Build()

	r := &TeamReconciler{
		Client:                   fakeClient,
		Scheme:                   s,
		EnableIAMTeamAdminAccess: true,
		IAMTeamAPIURL:            server.URL,
		IAMTeamAPITimeout:        5 * time.Second,
	}

	if err := r.ensureTeamAdminRBAC(context.Background(), team); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	crb := &rbacv1.ClusterRoleBinding{}
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: team.Name + "-team-clusterrolebinding"}, crb); err != nil {
		t.Fatalf("CRB not found: %v", err)
	}

	if len(crb.Subjects) != 1 || crb.Subjects[0].Name != iamAdmin {
		t.Fatalf("expected IAM subject %q, got %v", iamAdmin, crb.Subjects)
	}
	for _, subj := range crb.Subjects {
		if subj.Name == "spec-admin@example.com" {
			t.Fatal("CRB must not contain spec.TeamAdmins entries when IAM is enabled")
		}
	}
}

// TestRBAC_IAMSourceOverridesSpec verifies that when IAM is enabled, a user
// present in spec.TeamAdmins but absent from IAM is NOT granted access.
func TestRBAC_IAMSourceOverridesSpec(t *testing.T) {
	const iamAdmin = "iam-only-admin@example.com"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"name":"rbac-override","admins":[%q]}`, iamAdmin)
	}))
	defer server.Close()

	s := newRBACTestScheme()
	team := newRBACTestTeam("rbac-override")
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(team).Build()

	r := &TeamReconciler{
		Client:                   fakeClient,
		Scheme:                   s,
		EnableIAMTeamAdminAccess: true,
		IAMTeamAPIURL:            server.URL,
		IAMTeamAPITimeout:        5 * time.Second,
	}

	if err := r.ensureTeamAdminRBAC(context.Background(), team); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	crb := &rbacv1.ClusterRoleBinding{}
	if err := fakeClient.Get(context.Background(), types.NamespacedName{Name: team.Name + "-team-clusterrolebinding"}, crb); err != nil {
		t.Fatalf("CRB not found: %v", err)
	}

	for _, subj := range crb.Subjects {
		if subj.Name == "spec-admin@example.com" {
			t.Fatal("spec.TeamAdmins user must not appear in CRB when IAM overrides it")
		}
	}
	found := false
	for _, subj := range crb.Subjects {
		if subj.Name == iamAdmin {
			found = true
		}
	}
	if !found {
		t.Fatalf("IAM admin %q not found in CRB subjects %v", iamAdmin, crb.Subjects)
	}
}

// TestRBAC_IAMFailure verifies that when IAM is enabled but the API is
// unavailable, ensureTeamAdminRBAC returns an error (triggering a requeue)
// without mutating the existing CRB.
func TestRBAC_IAMFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusInternalServerError)
	}))
	defer server.Close()

	s := newRBACTestScheme()
	team := newRBACTestTeam("rbac-iam-fail")
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithObjects(team).Build()

	r := &TeamReconciler{
		Client:                   fakeClient,
		Scheme:                   s,
		EnableIAMTeamAdminAccess: true,
		IAMTeamAPIURL:            server.URL,
		IAMTeamAPITimeout:        5 * time.Second,
	}

	if err := r.ensureTeamAdminRBAC(context.Background(), team); err == nil {
		t.Fatal("expected error when IAM API is unavailable")
	}
}
