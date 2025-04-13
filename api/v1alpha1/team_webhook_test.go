package v1alpha1

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("", func() {
	var (
		fooTeamName       = "foo-team"
		fooTeamAdminName  = "foo-admin"
		fooTeamNamespaces = []string{"default"}
	)
	var (
		err error
	)
	fooTeamTypeMeta := metav1.TypeMeta{
		APIVersion: "team.snappcloud.io/v1alpha1",
		Kind:       "Team",
	}
	fooTeamObjectMeta := metav1.ObjectMeta{
		Name: fooTeamName,
	}
	fooTeam := &Team{
		TypeMeta:   fooTeamTypeMeta,
		ObjectMeta: fooTeamObjectMeta,
	}

	BeforeEach(func() {
		// TODO: create user if not exists
	})

	AfterEach(func() {
		err = k8sClient.Delete(ctx, fooTeam)
		if err != nil {
			Expect(errors.IsNotFound(err)).Should(BeTrue())
		}
	})

	Context("When creating Team", func() {
		It("should fail if Namespace does not exist", func() {
			fooTeamTmp := &Team{
				TypeMeta:   fooTeam.TypeMeta,
				ObjectMeta: fooTeam.ObjectMeta,
				Spec: TeamSpec{
					TeamAdmin: fooTeamAdminName,
					Projects: []Project{
						{Name: "not-existing-namespace", EnvLabel: "staging"},
					},
				},
			}
			err = k8sClient.Create(ctx, fooTeamTmp)
			Expect(err).NotTo(BeNil())
		})

		It("should fail if TeamAdmin does not exist", func() {
			ns := &v1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "foo-namespace",
				},
			}
			err = k8sClient.Create(ctx, ns)
			Expect(err).To(BeNil())
			fooTeamTmp := &Team{
				TypeMeta:   fooTeam.TypeMeta,
				ObjectMeta: fooTeam.ObjectMeta,
				Spec: TeamSpec{
					TeamAdmin: "not-existing-team-admin",
					Projects: []Project{
						{Name: "foo-namespace"},
					},
				},
			}
			err = k8sClient.Create(ctx, fooTeamTmp)
			Expect(err).NotTo(BeNil())
		})

		It("should fail if at least one namespace has team label from another team", func() {
			ns := &corev1.Namespace{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: fooTeamNamespaces[0]}, ns)
			Expect(err).To(BeNil())
			patch := []byte(`{"metadata":{"labels":{"snappcloud.io/team": "non-existing-team"}}}`)
			err = k8sClient.Patch(ctx, ns, client.RawPatch(types.StrategicMergePatchType, patch))
			Expect(err).To(BeNil())
			fooTeamTmp := &Team{
				TypeMeta:   fooTeam.TypeMeta,
				ObjectMeta: fooTeam.ObjectMeta,
				Spec: TeamSpec{
					TeamAdmin: fooTeamAdminName,
					Projects:  []Project{{Name: fooTeamNamespaces[0]}},
				},
			}
			err = k8sClient.Create(ctx, fooTeamTmp)
			Expect(err).NotTo(BeNil())
		})
	})
})
