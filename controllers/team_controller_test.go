package controllers

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/snapp-incubator/team-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const (
	testTimeout  = 10 * time.Second
	testInterval = 250 * time.Millisecond
)

var (
	teamName   = "test-cloud"
	teamAdmins = []v1alpha1.Admin{{Name: "user-test"}}
	projects   = []v1alpha1.Project{
		{Name: "test-ns-1", EnvLabel: "staging"},
		{Name: "test-ns-2", EnvLabel: "production"},
	}
	updateProjects = []v1alpha1.Project{
		{Name: "test-ns-1", EnvLabel: "production"},
		{Name: "test-ns-2", EnvLabel: "staging"},
	}

	teamNameSA = "test-cloud-sa"
)

var _ = Describe("Testing Team", func() {
	ctx := context.Background()
	validTeamObj := &v1alpha1.Team{
		ObjectMeta: metav1.ObjectMeta{
			Name: teamName,
		},
		TypeMeta: metav1.TypeMeta{
			APIVersion: "team.snappcloud.io/v1alpha1",
			Kind:       "Team",
		},
		Spec: v1alpha1.TeamSpec{
			TeamAdmins: teamAdmins,
			Projects:   projects,
		},
	}

	BeforeEach(func() {
		// create namespaces
		for _, ns := range projects {
			nsObj := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: ns.Name,
				},
			}
			err := k8sClient.Create(ctx, nsObj)
			if err != nil {
				if !errors.IsAlreadyExists(err) {
					Expect(err).To(BeNil())
				}
			}
		}
	})

	Context("When creating and deleting Team", func() {
		It("should create metric namespace", func() {
			err := k8sClient.Create(ctx, validTeamObj)
			if err != nil && !errors.IsAlreadyExists(err) {
				Expect(err).To(BeNil())
			}

			metricNS := &corev1.Namespace{}
			metricNSName := types.NamespacedName{
				Name: teamName + MetricNamespaceSuffix,
			}
			Eventually(func() error {
				return k8sClient.Get(ctx, metricNSName, metricNS)
			}, testTimeout, testInterval).Should(Succeed())
		})

		It("all namespaces should have the team label and correct environment", func() {
			for _, ns := range projects {
				nsObj := &corev1.Namespace{}
				errNS := k8sClient.Get(ctx, types.NamespacedName{Name: ns.Name}, nsObj)
				Expect(errNS).To(BeNil())
				Expect(nsObj.ObjectMeta.Labels["snappcloud.io/team"]).To(Equal(teamName))
				Expect(nsObj.ObjectMeta.Labels[MetaDataLabelEnv]).To(Equal(ns.EnvLabel))

				nsMetricObj := &corev1.Namespace{}
				errMetric := k8sClient.Get(ctx, types.NamespacedName{Name: teamName + MetricNamespaceSuffix}, nsMetricObj)
				Expect(errMetric).To(BeNil())
				Expect(nsMetricObj.ObjectMeta.Labels["snappcloud.io/team"]).To(Equal(teamName))
			}
		})

		It("after updating team object, new envLabels should be applied", func() {
			var updateTeam = &v1alpha1.Team{}
			errGetTeam := k8sClient.Get(ctx, types.NamespacedName{Name: teamName}, updateTeam)
			Expect(errGetTeam).To(BeNil())

			updateTeam.Spec.Projects = updateProjects
			errUpdateTeam := k8sClient.Update(ctx, updateTeam)
			Expect(errUpdateTeam).To(BeNil())

			for _, ns := range updateProjects {
				nsCopy := ns
				Eventually(func() string {
					nsObj := &corev1.Namespace{}
					_ = k8sClient.Get(ctx, types.NamespacedName{Name: nsCopy.Name}, nsObj)
					return nsObj.Labels[MetaDataLabelEnv]
				}, testTimeout, testInterval).Should(Equal(nsCopy.EnvLabel))

				nsObj := &corev1.Namespace{}
				Expect(k8sClient.Get(ctx, types.NamespacedName{Name: nsCopy.Name}, nsObj)).To(Succeed())
				Expect(nsObj.Labels["snappcloud.io/team"]).To(Equal(teamName))
			}
		})

		It("should create ClusterRole for team admins", func() {
			cr := &rbacv1.ClusterRole{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: teamName + "-team-clusterrole"}, cr)).To(Succeed())
			Expect(cr.Rules).To(HaveLen(1))
			Expect(cr.Rules[0].Resources).To(ContainElement("teams"))
			Expect(cr.Rules[0].ResourceNames).To(ContainElement(teamName))
			Expect(cr.Rules[0].Verbs).To(ConsistOf("get", "patch", "update"))
			Expect(cr.OwnerReferences).To(HaveLen(1))
			Expect(cr.OwnerReferences[0].Name).To(Equal(teamName))
		})

		It("should create ClusterRoleBinding with User subject for plain admin", func() {
			crb := &rbacv1.ClusterRoleBinding{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: teamName + "-team-clusterrolebinding"}, crb)).To(Succeed())
			Expect(crb.RoleRef.Name).To(Equal(teamName + "-team-clusterrole"))
			Expect(crb.Subjects).To(ContainElement(rbacv1.Subject{
				Kind:     "User",
				APIGroup: "rbac.authorization.k8s.io",
				Name:     "user-test",
			}))
		})

		It("should update CRB subjects when TeamAdmins is changed", func() {
			team := &v1alpha1.Team{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: teamName}, team)).To(Succeed())
			team.Spec.TeamAdmins = []v1alpha1.Admin{{Name: "updated-admin@example.com"}}
			Expect(k8sClient.Update(ctx, team)).To(Succeed())

			Eventually(func() []rbacv1.Subject {
				crb := &rbacv1.ClusterRoleBinding{}
				if err := k8sClient.Get(ctx, types.NamespacedName{Name: teamName + "-team-clusterrolebinding"}, crb); err != nil {
					return nil
				}
				return crb.Subjects
			}, testTimeout, testInterval).Should(ConsistOf(rbacv1.Subject{
				Kind:     "User",
				APIGroup: "rbac.authorization.k8s.io",
				Name:     "updated-admin@example.com",
			}))
		})

		It("should re-create CRB when it is deleted externally", func() {
			crb := &rbacv1.ClusterRoleBinding{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: teamName + "-team-clusterrolebinding"}, crb)).To(Succeed())
			Expect(k8sClient.Delete(ctx, crb)).To(Succeed())

			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{Name: teamName + "-team-clusterrolebinding"}, &rbacv1.ClusterRoleBinding{})
			}, testTimeout, testInterval).Should(Succeed())
		})

		It("should revert CRB subjects when modified externally", func() {
			crb := &rbacv1.ClusterRoleBinding{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: teamName + "-team-clusterrolebinding"}, crb)).To(Succeed())
			crb.Subjects = nil
			Expect(k8sClient.Update(ctx, crb)).To(Succeed())

			Eventually(func() []rbacv1.Subject {
				fresh := &rbacv1.ClusterRoleBinding{}
				if err := k8sClient.Get(ctx, types.NamespacedName{Name: teamName + "-team-clusterrolebinding"}, fresh); err != nil {
					return nil
				}
				return fresh.Subjects
			}, testTimeout, testInterval).Should(ContainElement(rbacv1.Subject{
				Kind:     "User",
				APIGroup: "rbac.authorization.k8s.io",
				Name:     "updated-admin@example.com",
			}))
		})

		It("should re-create CR when it is deleted externally", func() {
			cr := &rbacv1.ClusterRole{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: teamName + "-team-clusterrole"}, cr)).To(Succeed())
			Expect(k8sClient.Delete(ctx, cr)).To(Succeed())

			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{Name: teamName + "-team-clusterrole"}, &rbacv1.ClusterRole{})
			}, testTimeout, testInterval).Should(Succeed())
		})

		It("should revert CR rules when modified externally", func() {
			cr := &rbacv1.ClusterRole{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: teamName + "-team-clusterrole"}, cr)).To(Succeed())
			cr.Rules = nil
			Expect(k8sClient.Update(ctx, cr)).To(Succeed())

			Eventually(func() []rbacv1.PolicyRule {
				fresh := &rbacv1.ClusterRole{}
				if err := k8sClient.Get(ctx, types.NamespacedName{Name: teamName + "-team-clusterrole"}, fresh); err != nil {
					return nil
				}
				return fresh.Rules
			}, testTimeout, testInterval).Should(ContainElement(rbacv1.PolicyRule{
				APIGroups:     []string{"team.snappcloud.io"},
				Resources:     []string{"teams"},
				ResourceNames: []string{teamName},
				Verbs:         []string{"get", "patch", "update"},
			}))
		})

		It("should delete CRB, CR and metric namespace when Team is deleted", func() {
			err := k8sClient.Delete(ctx, validTeamObj)
			Expect(err).To(BeNil())

			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: teamName + "-team-clusterrolebinding"}, &rbacv1.ClusterRoleBinding{})
				return errors.IsNotFound(err)
			}, testTimeout, testInterval).Should(BeTrue())

			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: teamName + "-team-clusterrole"}, &rbacv1.ClusterRole{})
				return errors.IsNotFound(err)
			}, testTimeout, testInterval).Should(BeTrue())

			Eventually(func() bool {
				metricNS := &corev1.Namespace{}
				err := k8sClient.Get(ctx, types.NamespacedName{Name: teamName + MetricNamespaceSuffix}, metricNS)
				return errors.IsNotFound(err) || (err == nil && metricNS.Status.Phase == corev1.NamespaceTerminating)
			}, testTimeout, testInterval).Should(BeTrue())
		})
	})

	Context("When team admin is a service account", func() {
		saTeamObj := &v1alpha1.Team{
			ObjectMeta: metav1.ObjectMeta{
				Name: teamNameSA,
			},
			TypeMeta: metav1.TypeMeta{
				APIVersion: "team.snappcloud.io/v1alpha1",
				Kind:       "Team",
			},
			Spec: v1alpha1.TeamSpec{
				TeamAdmins: []v1alpha1.Admin{{Name: "system:serviceaccount:infra:my-sa"}},
			},
		}

		It("should create ClusterRoleBinding with ServiceAccount subject", func() {
			err := k8sClient.Create(ctx, saTeamObj)
			if err != nil && !errors.IsAlreadyExists(err) {
				Expect(err).To(BeNil())
			}

			crb := &rbacv1.ClusterRoleBinding{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{Name: teamNameSA + "-team-clusterrolebinding"}, crb)
			}, testTimeout, testInterval).Should(Succeed())
			Expect(crb.Subjects).To(ContainElement(rbacv1.Subject{
				Kind:      "ServiceAccount",
				Namespace: "infra",
				Name:      "my-sa",
			}))
		})
	})
})
