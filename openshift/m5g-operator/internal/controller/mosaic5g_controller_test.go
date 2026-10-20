/*
Copyright 2026.

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

package controller

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/yaml"

	mosaic5gv1alpha1 "github.com/kubedge/kube5g/openshift/m5g-operator/api/v1alpha1"
)

const crName = "mosaic5g"

var _ = Describe("Mosaic5g Controller", func() {
	var (
		ctx       context.Context
		namespace string
		key       types.NamespacedName
		r         *Mosaic5gReconciler
		nsCounter int
	)

	reconcileOnce := func() reconcile.Result {
		res, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		return res
	}

	getDeployment := func(name string) *appsv1.Deployment {
		dep := &appsv1.Deployment{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, dep)).To(Succeed())
		return dep
	}

	// envtest runs no controllers, so readiness has to be faked.
	markReady := func(name string) {
		dep := getDeployment(name)
		dep.Status.Replicas = 1
		dep.Status.ReadyReplicas = 1
		Expect(k8sClient.Status().Update(ctx, dep)).To(Succeed())
	}

	BeforeEach(func() {
		ctx = context.Background()
		nsCounter++
		namespace = fmt.Sprintf("mosaic5g-test-%d", nsCounter)
		Expect(k8sClient.Create(ctx, &corev1.Namespace{Name: namespace})).To(Succeed())

		key = types.NamespacedName{Name: crName, Namespace: namespace}
		Expect(k8sClient.Create(ctx, &mosaic5gv1alpha1.Mosaic5g{
			Name: key.Name, Namespace: key.Namespace,
			Spec: mosaic5gv1alpha1.Mosaic5gSpec{
				CNImage:  "mosaic5gecosys/oaicn:1.1",
				RANImage: "mosaic5gecosys/oairan:1.1",
				MCC:      "208",
				MNC:      "95",
				DNS:      "192.168.106.12",
			},
		})).To(Succeed())

		r = &Mosaic5gReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
	})

	It("applies CRD defaults", func() {
		m := &mosaic5gv1alpha1.Mosaic5g{}
		Expect(k8sClient.Get(ctx, key, m)).To(Succeed())
		Expect(m.Spec.Size).To(HaveValue(Equal(int32(1))))
		Expect(m.Spec.MmeDomainName).To(Equal("oaicn"))
		Expect(m.Spec.MysqlDomainName).To(Equal("mysql"))
		Expect(m.Spec.ConfigurationPathofCN).To(Equal("/var/snap/oai-cn/current/"))
	})

	It("creates MySQL, oai-cn and oai-ran in order", func() {
		By("creating the config, ServiceAccount and MySQL first")
		res := reconcileOnce()
		Expect(res.RequeueAfter).To(Equal(mysqlWait))

		cm := &corev1.ConfigMap{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: configMapName, Namespace: namespace}, cm)).To(Succeed())
		conf := map[string]any{}
		Expect(yaml.Unmarshal([]byte(cm.Data[configFileName]), &conf)).To(Succeed())
		Expect(conf).To(HaveKeyWithValue("mcc", "208"))
		Expect(conf).To(HaveKeyWithValue("mmeDomainName", "oaicn"))
		Expect(conf).To(HaveKeyWithValue("flexRAN", false))

		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: serviceAccountName, Namespace: namespace}, &corev1.ServiceAccount{})).To(Succeed())
		Expect(getDeployment(mysqlName).Spec.Template.Spec.Containers[0].Image).To(Equal(defaultMySQLImage))
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: mysqlName, Namespace: namespace}, &corev1.Service{})).To(Succeed())
		err := k8sClient.Get(ctx, types.NamespacedName{Name: "mosaic5g-oaicn", Namespace: namespace}, &appsv1.Deployment{})
		Expect(errors.IsNotFound(err)).To(BeTrue())

		m := &mosaic5gv1alpha1.Mosaic5g{}
		Expect(k8sClient.Get(ctx, key, m)).To(Succeed())
		cond := meta.FindStatusCondition(m.Status.Conditions, conditionAvailable)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Reason).To(Equal("WaitingForMySQL"))

		By("creating oai-cn once MySQL is ready")
		markReady(mysqlName)
		Expect(reconcileOnce().RequeueAfter).To(Equal(cnWait))
		cn := getDeployment("mosaic5g-oaicn")
		c := cn.Spec.Template.Spec.Containers[0]
		Expect(c.Image).To(Equal("mosaic5gecosys/oaicn:1.1"))
		Expect(*c.SecurityContext.Privileged).To(BeTrue())
		Expect(cn.Spec.Template.Spec.ServiceAccountName).To(Equal(serviceAccountName))
		Expect(cn.Spec.Template.Annotations).To(HaveKey(configHashAnnotation))
		Expect(cn.Spec.Strategy.Type).To(Equal(appsv1.RecreateDeploymentStrategyType))
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: cnServiceName, Namespace: namespace}, &corev1.Service{})).To(Succeed())

		By("creating oai-ran once oai-cn is ready")
		markReady("mosaic5g-oaicn")
		Expect(reconcileOnce().RequeueAfter).To(Equal(ranWait))
		ran := getDeployment("mosaic5g-oairan")
		Expect(ran.Spec.Selector.MatchLabels).To(Equal(map[string]string{appLabel: "mosaic5g", crLabel: crName}))

		By("reporting Available once oai-ran is ready")
		markReady("mosaic5g-oairan")
		Expect(reconcileOnce()).To(Equal(reconcile.Result{}))
		Expect(k8sClient.Get(ctx, key, m)).To(Succeed())
		Expect(meta.IsStatusConditionTrue(m.Status.Conditions, conditionAvailable)).To(BeTrue())

		By("not binding the privileged SCC on plain Kubernetes")
		err = k8sClient.Get(ctx, types.NamespacedName{Name: sccRoleBindingName, Namespace: namespace}, &rbacv1.RoleBinding{})
		Expect(errors.IsNotFound(err)).To(BeTrue())
	})

	It("rolls the pods when the configuration changes", func() {
		reconcileOnce()
		markReady(mysqlName)
		reconcileOnce()
		before := getDeployment("mosaic5g-oaicn").Spec.Template.Annotations[configHashAnnotation]

		m := &mosaic5gv1alpha1.Mosaic5g{}
		Expect(k8sClient.Get(ctx, key, m)).To(Succeed())
		m.Spec.CNImage = "mosaic5gecosys/oaicn:1.2"
		m.Spec.Size = new(int32(2))
		Expect(k8sClient.Update(ctx, m)).To(Succeed())
		reconcileOnce()

		cn := getDeployment("mosaic5g-oaicn")
		Expect(cn.Spec.Template.Annotations[configHashAnnotation]).NotTo(Equal(before))
		Expect(cn.Spec.Template.Spec.Containers[0].Image).To(Equal("mosaic5gecosys/oaicn:1.2"))
		Expect(*cn.Spec.Replicas).To(Equal(int32(2)))
	})

	It("is a no-op when nothing changed", func() {
		reconcileOnce()
		markReady(mysqlName)
		reconcileOnce()
		rv := getDeployment("mosaic5g-oaicn").ResourceVersion
		reconcileOnce()
		Expect(getDeployment("mosaic5g-oaicn").ResourceVersion).To(Equal(rv))
	})

	It("binds the privileged SCC on OpenShift", func() {
		r.OpenShift = true
		reconcileOnce()
		rb := &rbacv1.RoleBinding{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: sccRoleBindingName, Namespace: namespace}, rb)).To(Succeed())
		Expect(rb.RoleRef.Name).To(Equal(privilegedSCCClusterRole))
		Expect(rb.Subjects).To(ConsistOf(rbacv1.Subject{
			Kind: rbacv1.ServiceAccountKind, Name: serviceAccountName, Namespace: namespace,
		}))
	})

	It("ignores a deleted Mosaic5g", func() {
		Expect(k8sClient.Delete(ctx, &mosaic5gv1alpha1.Mosaic5g{
			Name: key.Name, Namespace: key.Namespace,
		})).To(Succeed())
		Expect(reconcileOnce()).To(Equal(reconcile.Result{}))
	})
})
