package controllers

import (
	"context"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/snapp-incubator/team-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"time"
)

var (
	namespaces = []v1alpha1.NamespaceDef{{Name: "test-ns-1", Environment: "production"}, {Name: "test-ns-2", Environment: "staging"}}
	teamAdmin  = "user-test"
	teamName   = "test-cloud"
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
			TeamAdmin:  teamAdmin,
			Namespaces: namespaces,
		},
	}

	BeforeEach(func() {
		// create namespaces
		for _, ns := range namespaces {
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

		It("all namespaces should have the team and datasource labels", func() {
			for _, ns := range namespaces {
				nsObj := &corev1.Namespace{}
				err := k8sClient.Get(ctx, types.NamespacedName{Name: ns.Name}, nsObj)
				Expect(err).To(BeNil())
				Expect(nsObj.ObjectMeta.Labels["snappcloud.io/team"]).To(Equal(teamName))
				Expect(nsObj.ObjectMeta.Labels["snappcloud.io/datasource"]).To(Equal("true"))
				Expect(nsObj.ObjectMeta.Labels["environment"]).To(Equal(ns.Environment))
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
