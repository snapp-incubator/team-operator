package controllers

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/snapp-incubator/team-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
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
})
