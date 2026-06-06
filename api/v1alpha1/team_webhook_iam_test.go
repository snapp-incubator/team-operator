package v1alpha1

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	iamTestTeamName     = "foo-team"
	iamTestNamespace    = "foo-namespace"
	iamTestCurrentUser  = "foo-admin@snapp.cab"
	iamTestOtherUser    = "other-admin@snapp.cab"
	iamTestSpecOnlyUser = "spec-admin@snapp.cab"
)

func TestIAMFetchTeamAdmin(t *testing.T) {
	tests := []struct {
		name        string
		handler     http.HandlerFunc
		wantAllowed bool
		wantErr     bool
	}{
		{
			name: "admin user",
			handler: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/"+iamTestTeamName {
					t.Fatalf("expected path /%s, got %s", iamTestTeamName, r.URL.Path)
				}
				_, _ = fmt.Fprintf(w, `{"name":%q,"admins":[%q]}`, iamTestTeamName, iamTestCurrentUser)
			},
			wantAllowed: true,
		},
		{
			name: "non-admin user",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = fmt.Fprintf(w, `{"name":%q,"members":[%q],"admins":[%q]}`, iamTestTeamName, iamTestCurrentUser, iamTestOtherUser)
			},
		},
		{
			name: "non-2xx response",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "not found", http.StatusNotFound)
			},
			wantErr: true,
		},
		{
			name: "malformed json",
			handler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{`))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(tt.handler)
			defer server.Close()

			allowed, err := fetchIAMTeamAdmin(context.TODO(), server.Client(), server.URL, iamTestTeamName, iamTestCurrentUser)
			if tt.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if allowed != tt.wantAllowed {
				t.Fatalf("expected allowed %t, got %t", tt.wantAllowed, allowed)
			}
		})
	}
}

func TestIAMTeamAdminAccess(t *testing.T) {
	team := &Team{
		ObjectMeta: metav1.ObjectMeta{Name: iamTestTeamName},
		Spec: TeamSpec{
			TeamAdmins: []Admin{{Name: iamTestSpecOnlyUser}},
		},
	}

	tests := []struct {
		name      string
		validator *teamValidator
		user      string
		wantErr   bool
	}{
		{
			name:      "feature enabled ignores spec teamAdmins when IAM succeeds",
			validator: iamTestValidator(t, []string{iamTestOtherUser}, http.StatusOK),
			user:      iamTestSpecOnlyUser,
			wantErr:   true,
		},
		{
			name:      "feature enabled allows IAM admins",
			validator: iamTestValidator(t, []string{iamTestCurrentUser}, http.StatusOK),
			user:      iamTestCurrentUser,
		},
		{
			name:      "feature enabled rejects members that are not admins",
			validator: iamTestValidator(t, nil, http.StatusOK),
			user:      iamTestCurrentUser,
			wantErr:   true,
		},
		{
			name:      "IAM failure falls back to spec teamAdmins",
			validator: iamTestValidator(t, nil, http.StatusInternalServerError),
			user:      iamTestSpecOnlyUser,
		},
		{
			name:      "operator service account remains allowed",
			validator: iamTestValidator(t, nil, http.StatusInternalServerError),
			user:      ServiceAccount,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.validator.iamTeamAdminAccess(team, iamTestNamespace, tt.user, func() error {
				if tt.user == iamTestSpecOnlyUser {
					return nil
				}
				return fmt.Errorf("fallback denied")
			})
			if tt.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func iamTestValidator(t *testing.T, admins []string, statusCode int) *teamValidator {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(statusCode)
		if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
			return
		}
		_, _ = fmt.Fprintf(w, `{"name":%q,"members":[%q],"admins":[`, iamTestTeamName, iamTestCurrentUser)
		for i, admin := range admins {
			if i > 0 {
				_, _ = w.Write([]byte(","))
			}
			_, _ = fmt.Fprintf(w, `%q`, admin)
		}
		_, _ = w.Write([]byte(`]}`))
	}))
	t.Cleanup(server.Close)

	return &teamValidator{
		enableIAMTeamAdminAccess: true,
		iamTeamAPIURL:            server.URL,
		iamTeamAPITimeout:        time.Second,
		iamTeamHTTPClient:        server.Client(),
	}
}
