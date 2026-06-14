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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// LLMSpec defines the desired state of LLM.
type LLMSpec struct {
	// Model is the HuggingFace model ID (e.g. "Qwen/Qwen2.5-1.5B-Instruct")
	// or a local path to the model weights to serve with vLLM.
	// +kubebuilder:validation:MinLength=1
	// +required
	Model string `json:"model"`

	// ServedModelName overrides the model name exposed via the OpenAI-compatible
	// API. Defaults to the value of Model when empty.
	// +optional
	ServedModelName string `json:"servedModelName,omitempty"`

	// Image is the vLLM server container image.
	// +kubebuilder:default="vllm/vllm-openai:latest"
	// +optional
	Image string `json:"image,omitempty"`

	// ImagePullPolicy for the vLLM container.
	// +kubebuilder:validation:Enum=Always;IfNotPresent;Never
	// +optional
	ImagePullPolicy corev1.PullPolicy `json:"imagePullPolicy,omitempty"`

	// ImagePullSecrets for pulling the vLLM image from private registries.
	// +optional
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

	// Replicas is the desired number of vLLM server replicas. This field is
	// ignored when Autoscaling is enabled. Defaults to 1.
	// +kubebuilder:validation:Minimum=0
	// +optional
	Replicas *int32 `json:"replicas,omitempty"`

	// Resources describes the compute resources for the vLLM container.
	// GPUs are requested here via the "nvidia.com/gpu" resource, e.g.
	// limits: { nvidia.com/gpu: 1 }.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// Args holds well-known vLLM engine arguments rendered onto the server
	// command line.
	// +optional
	Args VLLMArgs `json:"args,omitempty"`

	// ExtraArgs are additional raw flags appended verbatim to the vLLM server
	// command, e.g. ["--enable-prefix-caching", "--disable-log-requests"].
	// +optional
	ExtraArgs []string `json:"extraArgs,omitempty"`

	// Env are extra environment variables injected into the vLLM container.
	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`

	// HFTokenSecret references a Secret key holding a HuggingFace access token,
	// used to download gated or private models. It is exposed to the container
	// as the HUGGING_FACE_HUB_TOKEN environment variable.
	// +optional
	HFTokenSecret *corev1.SecretKeySelector `json:"hfTokenSecret,omitempty"`

	// ModelCache configures a PersistentVolumeClaim that caches downloaded model
	// weights so they survive restarts and rescheduling.
	// +optional
	ModelCache *ModelCacheSpec `json:"modelCache,omitempty"`

	// Autoscaling, when set, manages a HorizontalPodAutoscaler for the
	// deployment. While set, the Replicas field is not enforced.
	// +optional
	Autoscaling *AutoscalingSpec `json:"autoscaling,omitempty"`

	// Service configures the Service fronting the vLLM pods.
	// +optional
	Service ServiceSpec `json:"service,omitempty"`

	// Ingress, when set, exposes the service externally via an Ingress object.
	// +optional
	Ingress *IngressSpec `json:"ingress,omitempty"`

	// Monitoring configures Prometheus scraping of the vLLM /metrics endpoint.
	// +optional
	Monitoring *MonitoringSpec `json:"monitoring,omitempty"`

	// NodeSelector constrains scheduling, typically onto GPU nodes.
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// Tolerations allow scheduling onto tainted (e.g. GPU) nodes.
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// Affinity controls pod scheduling affinity/anti-affinity.
	// +optional
	Affinity *corev1.Affinity `json:"affinity,omitempty"`
}

// VLLMArgs holds first-class vLLM engine arguments. Anything not modeled here
// can be passed through LLMSpec.ExtraArgs.
type VLLMArgs struct {
	// TensorParallelSize sets --tensor-parallel-size, the number of GPUs each
	// replica shards the model across.
	// +kubebuilder:validation:Minimum=1
	// +optional
	TensorParallelSize *int32 `json:"tensorParallelSize,omitempty"`

	// PipelineParallelSize sets --pipeline-parallel-size.
	// +kubebuilder:validation:Minimum=1
	// +optional
	PipelineParallelSize *int32 `json:"pipelineParallelSize,omitempty"`

	// DType sets --dtype, the data type for model weights and activations.
	// +kubebuilder:validation:Enum=auto;half;float16;bfloat16;float32
	// +optional
	DType string `json:"dtype,omitempty"`

	// MaxModelLen sets --max-model-len, the maximum context length.
	// +kubebuilder:validation:Minimum=1
	// +optional
	MaxModelLen *int32 `json:"maxModelLen,omitempty"`

	// GPUMemoryUtilization sets --gpu-memory-utilization, the fraction (0-1] of
	// GPU memory reserved for the model executor, e.g. "0.9".
	// +kubebuilder:validation:Pattern=`^(0(\.[0-9]+)?|1(\.0+)?)$`
	// +optional
	GPUMemoryUtilization string `json:"gpuMemoryUtilization,omitempty"`

	// Quantization sets --quantization (e.g. awq, gptq, fp8).
	// +optional
	Quantization string `json:"quantization,omitempty"`

	// MaxNumSeqs sets --max-num-seqs, the maximum number of sequences batched
	// together per iteration.
	// +kubebuilder:validation:Minimum=1
	// +optional
	MaxNumSeqs *int32 `json:"maxNumSeqs,omitempty"`

	// TrustRemoteCode sets --trust-remote-code, required by some models that
	// ship custom modeling code.
	// +optional
	TrustRemoteCode *bool `json:"trustRemoteCode,omitempty"`
}

// ModelCacheSpec configures a PersistentVolumeClaim for caching model weights.
type ModelCacheSpec struct {
	// Size is the requested storage size for the cache PVC, e.g. "50Gi".
	// +kubebuilder:default="50Gi"
	// +optional
	Size resource.Quantity `json:"size,omitempty"`

	// StorageClassName for the cache PVC. Defaults to the cluster's default
	// StorageClass when empty.
	// +optional
	StorageClassName *string `json:"storageClassName,omitempty"`

	// MountPath is where the cache volume is mounted inside the container.
	// Defaults to the HuggingFace cache directory.
	// +kubebuilder:default="/root/.cache/huggingface"
	// +optional
	MountPath string `json:"mountPath,omitempty"`

	// AccessModes for the cache PVC. Defaults to ReadWriteOnce.
	// +optional
	AccessModes []corev1.PersistentVolumeAccessMode `json:"accessModes,omitempty"`
}

// AutoscalingSpec configures a HorizontalPodAutoscaler for the deployment.
type AutoscalingSpec struct {
	// MinReplicas is the lower bound for the autoscaler. Defaults to 1.
	// +kubebuilder:validation:Minimum=1
	// +optional
	MinReplicas *int32 `json:"minReplicas,omitempty"`

	// MaxReplicas is the upper bound for the autoscaler.
	// +kubebuilder:validation:Minimum=1
	// +required
	MaxReplicas int32 `json:"maxReplicas"`

	// TargetCPUUtilization is the target average CPU utilization percentage that
	// drives scaling. Requires the metrics-server.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	// +optional
	TargetCPUUtilization *int32 `json:"targetCPUUtilization,omitempty"`

	// TargetGPUUtilization is the target average GPU utilization percentage. It
	// requires a custom/external metrics source (e.g. the DCGM exporter) to be
	// available to the HPA.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	// +optional
	TargetGPUUtilization *int32 `json:"targetGPUUtilization,omitempty"`
}

// ServiceSpec configures the Service fronting the vLLM pods.
type ServiceSpec struct {
	// Port is the Service port exposing the vLLM OpenAI-compatible API.
	// +kubebuilder:default=8000
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +optional
	Port int32 `json:"port,omitempty"`

	// Type is the Service type. Defaults to ClusterIP.
	// +kubebuilder:validation:Enum=ClusterIP;NodePort;LoadBalancer
	// +kubebuilder:default=ClusterIP
	// +optional
	Type corev1.ServiceType `json:"type,omitempty"`
}

// IngressSpec exposes the service externally via an Ingress object.
type IngressSpec struct {
	// Host is the hostname routed to the vLLM service.
	// +kubebuilder:validation:MinLength=1
	// +required
	Host string `json:"host"`

	// ClassName selects the IngressClass to use.
	// +optional
	ClassName *string `json:"className,omitempty"`

	// Path is the HTTP path prefix to route. Defaults to "/".
	// +kubebuilder:default="/"
	// +optional
	Path string `json:"path,omitempty"`

	// TLSSecretName, when set, enables TLS termination using the named Secret.
	// +optional
	TLSSecretName string `json:"tlsSecretName,omitempty"`

	// Annotations are added to the generated Ingress object.
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`
}

// MonitoringSpec configures Prometheus scraping of the vLLM metrics endpoint.
type MonitoringSpec struct {
	// ServiceMonitor, when true, creates a Prometheus-Operator ServiceMonitor
	// targeting the vLLM /metrics endpoint. Requires the Prometheus Operator
	// CRDs to be installed in the cluster.
	// +optional
	ServiceMonitor bool `json:"serviceMonitor,omitempty"`

	// Interval is the scrape interval, e.g. "30s".
	// +kubebuilder:default="30s"
	// +optional
	Interval string `json:"interval,omitempty"`

	// Labels are added to the ServiceMonitor so a Prometheus instance can select
	// it via its serviceMonitorSelector.
	// +optional
	Labels map[string]string `json:"labels,omitempty"`
}

// LLMPhase is a high-level summary of an LLM's lifecycle state.
// +kubebuilder:validation:Enum=Pending;Progressing;Ready;Degraded
type LLMPhase string

const (
	// LLMPhasePending means the resource has been accepted but workloads are not
	// yet created or scheduled.
	LLMPhasePending LLMPhase = "Pending"
	// LLMPhaseProgressing means the deployment is rolling out or pods are still
	// loading the model.
	LLMPhaseProgressing LLMPhase = "Progressing"
	// LLMPhaseReady means at least one replica is serving and healthy.
	LLMPhaseReady LLMPhase = "Ready"
	// LLMPhaseDegraded means the resource failed to reach or maintain its
	// desired state.
	LLMPhaseDegraded LLMPhase = "Degraded"
)

// Condition types reported on the LLM status.
const (
	// ConditionAvailable is True when the LLM is serving traffic.
	ConditionAvailable = "Available"
	// ConditionProgressing is True while the LLM is being created or updated.
	ConditionProgressing = "Progressing"
	// ConditionDegraded is True when the LLM failed to reach its desired state.
	ConditionDegraded = "Degraded"
)

// LLMStatus defines the observed state of LLM.
type LLMStatus struct {
	// conditions represent the current state of the LLM resource. Standard
	// condition types are "Available", "Progressing", and "Degraded".
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Phase is a single-word summary of the LLM lifecycle state.
	// +optional
	Phase LLMPhase `json:"phase,omitempty"`

	// Replicas is the total number of non-terminated pods targeted by the
	// underlying deployment.
	// +optional
	Replicas int32 `json:"replicas,omitempty"`

	// AvailableReplicas is the number of pods that are ready and serving.
	// +optional
	AvailableReplicas int32 `json:"availableReplicas,omitempty"`

	// Endpoint is the in-cluster base URL of the OpenAI-compatible API.
	// +optional
	Endpoint string `json:"endpoint,omitempty"`

	// ObservedGeneration is the most recent generation observed by the
	// controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=vllm
// +kubebuilder:printcolumn:name="Model",type=string,JSONPath=`.spec.model`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Available",type=integer,JSONPath=`.status.availableReplicas`
// +kubebuilder:printcolumn:name="Endpoint",type=string,JSONPath=`.status.endpoint`,priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// LLM is the Schema for the llms API.
type LLM struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of LLM
	// +required
	Spec LLMSpec `json:"spec"`

	// status defines the observed state of LLM
	// +optional
	Status LLMStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// LLMList contains a list of LLM.
type LLMList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []LLM `json:"items"`
}

func init() {
	SchemeBuilder.Register(&LLM{}, &LLMList{})
}
