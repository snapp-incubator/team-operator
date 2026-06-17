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
			time.Sleep(5 * time.Second)
			err = k8sClient.Get(ctx, metricNSName, metricNS)
			Expect(err).To(BeNil())
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

			time.Sleep(5 * time.Second)
			for _, ns := range updateProjects {
				nsObj := &corev1.Namespace{}
				errNS := k8sClient.Get(ctx, types.NamespacedName{Name: ns.Name}, nsObj)
				Expect(errNS).To(BeNil())
				Expect(nsObj.ObjectMeta.Labels["snappcloud.io/team"]).To(Equal(teamName))
				Expect(nsObj.ObjectMeta.Labels[MetaDataLabelEnv]).To(Equal(ns.EnvLabel))
			}
		})

		It("should create ClusterRole for team admins", func() {
			cr := &rbacv1.ClusterRole{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: teamName + "-team-clusterrole"}, cr)).To(Succeed())
			Expect(cr.Rules).To(HaveLen(1))
			Expect(cr.Rules[0].Resources).To(ContainElement("teams"))
			Expect(cr.Rules[0].ResourceNames).To(ContainElement(teamName))
			Expect(cr.Rules[0].Verbs).To(ConsistOf("get", "list", "patch", "update"))
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

		It("should delete metric namespace", func() {
			err := k8sClient.Delete(ctx, validTeamObj)
			Expect(err).To(BeNil())
			time.Sleep(5 * time.Second)
			metricNS := &corev1.Namespace{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: teamName + MetricNamespaceSuffix}, metricNS)
			if err != nil || metricNS.Status.Phase != corev1.NamespaceTerminating {
				Expect(err).NotTo(BeNil())
			}
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
			time.Sleep(5 * time.Second)

			crb := &rbacv1.ClusterRoleBinding{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: teamNameSA + "-team-clusterrolebinding"}, crb)).To(Succeed())
			Expect(crb.Subjects).To(ContainElement(rbacv1.Subject{
				Kind:      "ServiceAccount",
				Namespace: "infra",
				Name:      "my-sa",
			}))
		})
	})
})
