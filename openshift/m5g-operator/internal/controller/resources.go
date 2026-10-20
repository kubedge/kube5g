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
	"crypto/sha256"
	"encoding/hex"
	"maps"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/yaml"

	mosaic5gv1alpha1 "github.com/kubedge/kube5g/openshift/m5g-operator/api/v1alpha1"
)

const (
	// Object names below are referenced by the *DomainName fields of the
	// default spec and by the hook inside the images, so they stay fixed.
	// As a consequence only one Mosaic5g is supported per namespace.
	configMapName      = "mosaic5g-config"
	configFileName     = "conf.yaml"
	configMountPath    = "/root/config"
	mysqlName          = "mysql"
	cnServiceName      = "oaicn"
	serviceAccountName = "mosaic5g"
	sccRoleBindingName = "mosaic5g-privileged-scc"

	// privilegedSCCClusterRole is the ClusterRole OpenShift ships to grant
	// "use" of the privileged SecurityContextConstraints.
	privilegedSCCClusterRole = "system:openshift:scc:privileged"

	defaultMySQLImage = "mysql:5.6"

	appLabel             = "app"
	crLabel              = "mosaic5g_cr"
	configHashAnnotation = "mosaic5g.com/config-hash"
)

// renderConfig renders the spec as the conf.yaml consumed by docker-hook.
func renderConfig(m *mosaic5gv1alpha1.Mosaic5g) (string, string, error) {
	data, err := yaml.Marshal(&m.Spec)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(data)
	return string(data), hex.EncodeToString(sum[:]), nil
}

func cnDeploymentName(m *mosaic5gv1alpha1.Mosaic5g) string {
	return m.Name + "-" + m.Spec.MmeDomainName
}

func ranDeploymentName(m *mosaic5gv1alpha1.Mosaic5g) string {
	return m.Name + "-oairan"
}

// Selector labels are kept identical to the original operator-sdk based
// operator so existing Deployments can be adopted (selectors are immutable).
func mysqlSelector() map[string]string { return map[string]string{appLabel: "oai"} }
func cnSelector() map[string]string    { return map[string]string{appLabel: "oaicn"} }
func ranSelector(m *mosaic5gv1alpha1.Mosaic5g) map[string]string {
	return map[string]string{appLabel: "mosaic5g", crLabel: m.Name}
}

// podLabels adds the CR label, used to list the pods for status, to a selector.
func podLabels(m *mosaic5gv1alpha1.Mosaic5g, selector map[string]string) map[string]string {
	labels := map[string]string{
		crLabel:                        m.Name,
		"app.kubernetes.io/managed-by": "m5g-operator",
	}
	maps.Copy(labels, selector)
	return labels
}

// genAffinity prefers nodes labelled oai=<tagValue> (cn or ran).
func genAffinity(tagValue string) *corev1.Affinity {
	return &corev1.Affinity{
		NodeAffinity: &corev1.NodeAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: []corev1.PreferredSchedulingTerm{{
				Weight: 10,
				Preference: corev1.NodeSelectorTerm{
					MatchExpressions: []corev1.NodeSelectorRequirement{{
						Key:      "oai",
						Operator: corev1.NodeSelectorOpIn,
						Values:   []string{tagValue},
					}},
				},
			}},
		},
	}
}

func hostPathVolume(name, path string) corev1.Volume {
	return corev1.Volume{
		Name: name,
		HostPath: &corev1.HostPathVolumeSource{
			Path: path,
			Type: ptr.To(corev1.HostPathDirectory),
		},
	}
}

// oaiPodSpec builds the systemd-in-container pod used by oai-cn and oai-ran.
func oaiPodSpec(name, image, affinity string, extraMounts []corev1.VolumeMount, extraVolumes []corev1.Volume) corev1.PodSpec {
	mounts := append([]corev1.VolumeMount{
		{Name: "cgroup", ReadOnly: true, MountPath: "/sys/fs/cgroup/"},
		{Name: "module", ReadOnly: true, MountPath: "/lib/modules/"},
		{Name: configMapName, MountPath: configMountPath},
	}, extraMounts...)
	volumes := append([]corev1.Volume{
		hostPathVolume("cgroup", "/sys/fs/cgroup/"),
		hostPathVolume("module", "/lib/modules/"),
		{
			Name: configMapName,
			ConfigMap: &corev1.ConfigMapVolumeSource{
				Name: configMapName,
			},
		},
	}, extraVolumes...)
	return corev1.PodSpec{
		ServiceAccountName: serviceAccountName,
		Affinity:           genAffinity(affinity),
		Volumes:            volumes,
		Containers: []corev1.Container{{
			Name:    name,
			Image:   image,
			Command: []string{"/sbin/init"},
			SecurityContext: &corev1.SecurityContext{
				Privileged:      new(true),
				AppArmorProfile: &corev1.AppArmorProfile{Type: corev1.AppArmorProfileTypeUnconfined},
			},
			VolumeMounts: mounts,
			Ports: []corev1.ContainerPort{{
				ContainerPort: 80,
				Name:          "mosaic5g-" + affinity,
			}},
		}},
	}
}

// mutateDeployment sets the fields of dep owned by the operator, leaving the
// ones defaulted by the API server alone so updates are no-ops when nothing
// changed.
func mutateDeployment(dep *appsv1.Deployment, m *mosaic5gv1alpha1.Mosaic5g, replicas int32,
	selector map[string]string, configHash string, podSpec corev1.PodSpec) {
	labels := podLabels(m, selector)
	dep.Labels = labels
	dep.Spec.Replicas = new(replicas)
	if dep.Spec.Selector == nil {
		dep.Spec.Selector = &metav1.LabelSelector{MatchLabels: selector}
	}
	// The pods use host devices (USRP) and hostPath mounts, so never run
	// two generations side by side.
	dep.Spec.Strategy = appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType}
	dep.Spec.Template.Labels = labels
	if dep.Spec.Template.Annotations == nil {
		dep.Spec.Template.Annotations = map[string]string{}
	}
	if configHash != "" {
		dep.Spec.Template.Annotations[configHashAnnotation] = configHash
	}

	// Merge the desired container into the existing one so server-side
	// defaults (terminationMessagePath, imagePullPolicy, ...) are preserved.
	existing := dep.Spec.Template.Spec
	desired := podSpec
	if len(existing.Containers) == 1 && existing.Containers[0].Name == desired.Containers[0].Name {
		c := existing.Containers[0]
		d := desired.Containers[0]
		c.Image = d.Image
		c.Command = d.Command
		c.Env = d.Env
		c.Ports = d.Ports
		c.SecurityContext = d.SecurityContext
		c.VolumeMounts = d.VolumeMounts
		desired.Containers = []corev1.Container{c}
	}
	existing.Containers = desired.Containers
	existing.Volumes = mergeVolumes(existing.Volumes, desired.Volumes)
	existing.Affinity = desired.Affinity
	existing.ServiceAccountName = desired.ServiceAccountName
	dep.Spec.Template.Spec = existing
}

// mergeVolumes returns desired, reusing existing entries with the same name so
// fields defaulted by the API server (e.g. ConfigMap defaultMode) are kept.
func mergeVolumes(existing, desired []corev1.Volume) []corev1.Volume {
	byName := make(map[string]corev1.Volume, len(existing))
	for _, v := range existing {
		byName[v.Name] = v
	}
	out := make([]corev1.Volume, 0, len(desired))
	for _, d := range desired {
		if e, ok := byName[d.Name]; ok {
			switch {
			case d.HostPath != nil && e.HostPath != nil:
				e.HostPath.Path = d.HostPath.Path
				e.HostPath.Type = d.HostPath.Type
				out = append(out, e)
				continue
			case d.ConfigMap != nil && e.ConfigMap != nil:
				e.ConfigMap.Name = d.ConfigMap.Name
				out = append(out, e)
				continue
			}
		}
		out = append(out, d)
	}
	return out
}

func mysqlPodSpec(m *mosaic5gv1alpha1.Mosaic5g) corev1.PodSpec {
	image := m.Spec.MysqlImage
	if image == "" {
		image = defaultMySQLImage
	}
	return corev1.PodSpec{
		ServiceAccountName: serviceAccountName,
		Affinity:           genAffinity("cn"),
		Containers: []corev1.Container{{
			Name:  "mysql",
			Image: image,
			// The oai-cn snap's HSS is configured with this password.
			Env: []corev1.EnvVar{{Name: "MYSQL_ROOT_PASSWORD", Value: "linux"}},
			Ports: []corev1.ContainerPort{{
				ContainerPort: 3306,
				Name:          "mysql",
			}},
		}},
	}
}

func cnPodSpec(m *mosaic5gv1alpha1.Mosaic5g) corev1.PodSpec {
	return oaiPodSpec("oaicn", m.Spec.CNImage, "cn", nil, nil)
}

func ranPodSpec(m *mosaic5gv1alpha1.Mosaic5g) corev1.PodSpec {
	return oaiPodSpec("oairan", m.Spec.RANImage, "ran",
		[]corev1.VolumeMount{{Name: "usrp", ReadOnly: true, MountPath: "/dev/bus/usb/"}},
		[]corev1.Volume{hostPathVolume("usrp", "/dev/bus/usb/")})
}

// mutateHeadlessService sets a headless Service's selector and ports.
func mutateHeadlessService(svc *corev1.Service, m *mosaic5gv1alpha1.Mosaic5g,
	selector map[string]string, ports []corev1.ServicePort) {
	svc.Labels = podLabels(m, nil)
	svc.Spec.ClusterIP = corev1.ClusterIPNone
	svc.Spec.Selector = selector
	for i := range ports {
		if ports[i].Protocol == "" {
			ports[i].Protocol = corev1.ProtocolTCP
		}
	}
	svc.Spec.Ports = ports
}

func cnServicePorts() []corev1.ServicePort {
	return []corev1.ServicePort{
		{Name: "enb", Port: 2152},
		{Name: "hss-1", Port: 3868},
		{Name: "hss-2", Port: 5868},
		{Name: "mme", Port: 2123},
		{Name: "spgw-1", Port: 3870},
		{Name: "spgw-2", Port: 5870},
	}
}

func mysqlServicePorts() []corev1.ServicePort {
	return []corev1.ServicePort{{Name: "mysql-port", Port: 3306}}
}

// mutateSCCRoleBinding grants the pods' ServiceAccount the privileged SCC.
func mutateSCCRoleBinding(rb *rbacv1.RoleBinding, m *mosaic5gv1alpha1.Mosaic5g) {
	rb.Labels = podLabels(m, nil)
	rb.RoleRef = rbacv1.RoleRef{
		APIGroup: rbacv1.GroupName,
		Kind:     "ClusterRole",
		Name:     privilegedSCCClusterRole,
	}
	rb.Subjects = []rbacv1.Subject{{
		Kind:      rbacv1.ServiceAccountKind,
		Name:      serviceAccountName,
		Namespace: m.Namespace,
	}}
}
