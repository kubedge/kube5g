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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// Mosaic5gSpec defines the desired state of Mosaic5g.
//
// The whole spec is rendered as conf.yaml into the ConfigMap mounted at
// /root/config in the OAI pods, where docker-hook reads it. The JSON field
// names therefore must stay in sync with
// dockers/docker-hook/internal/pkg/common/cfg.go.
type Mosaic5gSpec struct {
	// size is the number of oai-cn replicas.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=1
	// +optional
	Size *int32 `json:"size,omitempty"`

	// cnImage is the oai-cn container image.
	// +kubebuilder:validation:MinLength=1
	// +required
	CNImage string `json:"cnImage"`

	// ranImage is the oai-ran container image.
	// +kubebuilder:validation:MinLength=1
	// +required
	RANImage string `json:"ranImage"`

	// mysqlImage is the MySQL image used by the HSS.
	// +kubebuilder:default="mysql:5.6"
	// +optional
	MysqlImage string `json:"mysqlImage,omitempty"`

	// mcc is the PLMN mobile country code.
	// +required
	MCC string `json:"mcc"`

	// mnc is the PLMN mobile network code.
	// +required
	MNC string `json:"mnc"`

	// eutraBand is the E-UTRA band of the eNB.
	// +kubebuilder:default="7"
	// +optional
	EutraBand string `json:"eutraBand,omitempty"`

	// downlinkFrequency is the eNB downlink frequency, e.g. "2685000000L".
	// +kubebuilder:default="2685000000L"
	// +optional
	DownlinkFrequency string `json:"downlinkFrequency,omitempty"`

	// uplinkFrequencyOffset is the eNB uplink frequency offset, e.g. "-120000000".
	// +kubebuilder:default="-120000000"
	// +optional
	UplinkFrequencyOffset string `json:"uplinkFrequencyOffset,omitempty"`

	// flexRAN enables the FlexRAN agent in the eNB.
	// +optional
	FlexRAN bool `json:"flexRAN"`

	// configurationPathofCN is the oai-cn snap configuration directory.
	// +kubebuilder:default="/var/snap/oai-cn/current/"
	// +optional
	ConfigurationPathofCN string `json:"configurationPathofCN,omitempty"`

	// configurationPathofRAN is the oai-ran snap configuration directory.
	// +kubebuilder:default="/var/snap/oai-ran/current/"
	// +optional
	ConfigurationPathofRAN string `json:"configurationPathofRAN,omitempty"`

	// snapBinaryPath is the directory holding the snap command wrappers.
	// +kubebuilder:default="/snap/bin/"
	// +optional
	SnapBinaryPath string `json:"snapBinaryPath,omitempty"`

	// dns is the DNS server handed to UEs by the SPGW.
	// +optional
	DNS string `json:"dns"`

	// hssDomainName is the DNS name of the HSS.
	// +kubebuilder:default="oaicn"
	// +optional
	HssDomainName string `json:"hssDomainName,omitempty"`

	// mmeDomainName is the DNS name of the MME. It is also used to name the
	// oai-cn Deployment.
	// +kubebuilder:default="oaicn"
	// +optional
	MmeDomainName string `json:"mmeDomainName,omitempty"`

	// spgwDomainName is the DNS name of the SPGW.
	// +kubebuilder:default="oaicn"
	// +optional
	SpgwDomainName string `json:"spgwDomainName,omitempty"`

	// mysqlDomainName is the DNS name of the MySQL database.
	// +kubebuilder:default="mysql"
	// +optional
	MysqlDomainName string `json:"mysqlDomainName,omitempty"`

	// flexRANDomainName is the DNS name of the FlexRAN controller.
	// +kubebuilder:default="flexran"
	// +optional
	FlexRANDomainName string `json:"flexRANDomainName,omitempty"`
}

// Mosaic5gStatus defines the observed state of Mosaic5g.
type Mosaic5gStatus struct {
	// nodes lists the names of the pods managed by this Mosaic5g.
	// +listType=atomic
	// +optional
	Nodes []string `json:"nodes,omitempty"`

	// conditions represent the current state of the Mosaic5g resource.
	// "Available" is True once MySQL, oai-cn and oai-ran are all ready.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="CN Image",type=string,JSONPath=`.spec.cnImage`
// +kubebuilder:printcolumn:name="RAN Image",type=string,JSONPath=`.spec.ranImage`
// +kubebuilder:printcolumn:name="Available",type=string,JSONPath=`.status.conditions[?(@.type=="Available")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Mosaic5g is the Schema for the mosaic5gs API
type Mosaic5g struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Mosaic5g
	// +required
	Spec Mosaic5gSpec `json:"spec"`

	// status defines the observed state of Mosaic5g
	// +optional
	Status Mosaic5gStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// Mosaic5gList contains a list of Mosaic5g
type Mosaic5gList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Mosaic5g `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Mosaic5g{}, &Mosaic5gList{})
		return nil
	})
}
