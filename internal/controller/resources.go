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
	"fmt"
	"strconv"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	llmv1alpha1 "github.com/youngcheor/vllm-operator/api/v1alpha1"
)

const (
	// defaultImage is used when LLMSpec.Image is empty.
	defaultImage = "vllm/vllm-openai:latest"
	// defaultPort is the vLLM OpenAI-compatible API port.
	defaultPort = int32(8000)
	// containerName is the name of the vLLM container.
	containerName = "vllm"
	// httpPortName is the named port exposing the API (and /metrics).
	httpPortName = "http"
	// cacheVolumeName is the name of the model cache volume.
	cacheVolumeName = "model-cache"
	// defaultCacheMountPath is the default in-container path for the model cache,
	// matching the HuggingFace cache location.
	defaultCacheMountPath = "/root/.cache/huggingface"
	// defaultCacheSize is the default model cache PVC size.
	defaultCacheSize = "50Gi"
	// gpuMetricName is the per-pod GPU utilization metric used for autoscaling,
	// as exported by the NVIDIA DCGM exporter.
	gpuMetricName = "DCGM_FI_DEV_GPU_UTIL"
	// defaultCPUUtilization is the CPU target used when autoscaling is enabled
	// but no metric target is specified.
	defaultCPUUtilization = int32(80)
)

// labelsFor returns the full label set applied to all child objects.
func labelsFor(llm *llmv1alpha1.LLM) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "vllm",
		"app.kubernetes.io/instance":   llm.Name,
		"app.kubernetes.io/component":  "inference-server",
		"app.kubernetes.io/managed-by": "vllm-operator",
	}
}

// selectorLabelsFor returns the immutable subset of labels used as the
// Deployment/Service pod selector.
func selectorLabelsFor(llm *llmv1alpha1.LLM) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":     "vllm",
		"app.kubernetes.io/instance": llm.Name,
	}
}

// servedModelName returns the name exposed via the OpenAI API, defaulting to
// the model identifier.
func servedModelName(llm *llmv1alpha1.LLM) string {
	if llm.Spec.ServedModelName != "" {
		return llm.Spec.ServedModelName
	}
	return llm.Spec.Model
}

// vllmPort returns the configured service/container port, defaulting to 8000.
func vllmPort(llm *llmv1alpha1.LLM) int32 {
	if llm.Spec.Service.Port != 0 {
		return llm.Spec.Service.Port
	}
	return defaultPort
}

// image returns the container image, defaulting when unset.
func image(llm *llmv1alpha1.LLM) string {
	if llm.Spec.Image != "" {
		return llm.Spec.Image
	}
	return defaultImage
}

// effectiveReplicas returns the desired replica count. While autoscaling is
// enabled the replica count is owned by the HorizontalPodAutoscaler, so this
// returns nil to avoid the controller fighting the HPA.
func effectiveReplicas(llm *llmv1alpha1.LLM) *int32 {
	if llm.Spec.Autoscaling != nil {
		return nil
	}
	if llm.Spec.Replicas != nil {
		return llm.Spec.Replicas
	}
	return ptr.To(int32(1))
}

// endpointFor returns the in-cluster base URL of the OpenAI-compatible API.
func endpointFor(llm *llmv1alpha1.LLM) string {
	return fmt.Sprintf("http://%s.%s.svc:%d/v1", llm.Name, llm.Namespace, vllmPort(llm))
}

// pvcName returns the name of the model cache PVC for the given LLM.
func pvcName(llm *llmv1alpha1.LLM) string {
	return llm.Name + "-cache"
}

// cacheMountPath returns the in-container mount path for the model cache,
// defaulting to the HuggingFace cache directory.
func cacheMountPath(llm *llmv1alpha1.LLM) string {
	if llm.Spec.ModelCache != nil && llm.Spec.ModelCache.MountPath != "" {
		return llm.Spec.ModelCache.MountPath
	}
	return defaultCacheMountPath
}

// buildArgs renders the vLLM server command-line arguments from the spec.
func buildArgs(llm *llmv1alpha1.LLM) []string {
	args := []string{
		"--model", llm.Spec.Model,
		"--served-model-name", servedModelName(llm),
		"--port", strconv.Itoa(int(vllmPort(llm))),
	}

	a := llm.Spec.Args
	if a.TensorParallelSize != nil {
		args = append(args, "--tensor-parallel-size", strconv.Itoa(int(*a.TensorParallelSize)))
	}
	if a.PipelineParallelSize != nil {
		args = append(args, "--pipeline-parallel-size", strconv.Itoa(int(*a.PipelineParallelSize)))
	}
	if a.DType != "" {
		args = append(args, "--dtype", a.DType)
	}
	if a.MaxModelLen != nil {
		args = append(args, "--max-model-len", strconv.Itoa(int(*a.MaxModelLen)))
	}
	if a.GPUMemoryUtilization != "" {
		args = append(args, "--gpu-memory-utilization", a.GPUMemoryUtilization)
	}
	if a.Quantization != "" {
		args = append(args, "--quantization", a.Quantization)
	}
	if a.MaxNumSeqs != nil {
		args = append(args, "--max-num-seqs", strconv.Itoa(int(*a.MaxNumSeqs)))
	}
	if a.TrustRemoteCode != nil && *a.TrustRemoteCode {
		args = append(args, "--trust-remote-code")
	}

	args = append(args, llm.Spec.ExtraArgs...)
	return args
}

// buildEnv assembles the container environment, injecting the HuggingFace token
// from the referenced Secret when configured.
func buildEnv(llm *llmv1alpha1.LLM) []corev1.EnvVar {
	var env []corev1.EnvVar
	if llm.Spec.HFTokenSecret != nil {
		env = append(env, corev1.EnvVar{
			Name:      "HUGGING_FACE_HUB_TOKEN",
			ValueFrom: &corev1.EnvVarSource{SecretKeyRef: llm.Spec.HFTokenSecret},
		})
	}
	// When a cache is mounted at a non-default location, point HuggingFace at it.
	if llm.Spec.ModelCache != nil {
		env = append(env, corev1.EnvVar{Name: "HF_HOME", Value: cacheMountPath(llm)})
	}
	env = append(env, llm.Spec.Env...)
	return env
}

// healthProbe builds an HTTP probe against the vLLM /health endpoint.
func healthProbe(port int32, period, failureThreshold int32) *corev1.Probe {
	return &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{
				Path: "/health",
				Port: intstr.FromInt32(port),
			},
		},
		PeriodSeconds:    period,
		FailureThreshold: failureThreshold,
	}
}

// buildPodSpec assembles the pod template spec for the vLLM server.
func buildPodSpec(llm *llmv1alpha1.LLM) corev1.PodSpec {
	port := vllmPort(llm)

	container := corev1.Container{
		Name:            containerName,
		Image:           image(llm),
		ImagePullPolicy: llm.Spec.ImagePullPolicy,
		Args:            buildArgs(llm),
		Env:             buildEnv(llm),
		Resources:       llm.Spec.Resources,
		Ports: []corev1.ContainerPort{{
			Name:          httpPortName,
			ContainerPort: port,
			Protocol:      corev1.ProtocolTCP,
		}},
		// Model loading can take several minutes; the startup probe gives the
		// server up to ~10 minutes before liveness/readiness take over.
		StartupProbe:   healthProbe(port, 10, 60),
		ReadinessProbe: healthProbe(port, 10, 3),
		LivenessProbe:  healthProbe(port, 20, 3),
	}

	var volumes []corev1.Volume
	if llm.Spec.ModelCache != nil {
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
			Name:      cacheVolumeName,
			MountPath: cacheMountPath(llm),
		})
		volumes = append(volumes, corev1.Volume{
			Name: cacheVolumeName,
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: pvcName(llm),
				},
			},
		})
	}

	return corev1.PodSpec{
		Containers:       []corev1.Container{container},
		Volumes:          volumes,
		NodeSelector:     llm.Spec.NodeSelector,
		Tolerations:      llm.Spec.Tolerations,
		Affinity:         llm.Spec.Affinity,
		ImagePullSecrets: llm.Spec.ImagePullSecrets,
	}
}

// buildPVC returns the desired model cache PersistentVolumeClaim for the LLM.
func buildPVC(llm *llmv1alpha1.LLM) *corev1.PersistentVolumeClaim {
	cache := llm.Spec.ModelCache

	accessModes := cache.AccessModes
	if len(accessModes) == 0 {
		accessModes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
	}

	size := cache.Size
	if size.IsZero() {
		size = resource.MustParse(defaultCacheSize)
	}

	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvcName(llm),
			Namespace: llm.Namespace,
			Labels:    labelsFor(llm),
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      accessModes,
			StorageClassName: cache.StorageClassName,
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: size},
			},
		},
	}
}

// buildDeployment returns the desired Deployment for the given LLM.
func buildDeployment(llm *llmv1alpha1.LLM) *appsv1.Deployment {
	labels := labelsFor(llm)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      llm.Name,
			Namespace: llm.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: effectiveReplicas(llm),
			Selector: &metav1.LabelSelector{MatchLabels: selectorLabelsFor(llm)},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec:       buildPodSpec(llm),
			},
		},
	}
}

// buildService returns the desired Service for the given LLM.
func buildService(llm *llmv1alpha1.LLM) *corev1.Service {
	port := vllmPort(llm)
	svcType := llm.Spec.Service.Type
	if svcType == "" {
		svcType = corev1.ServiceTypeClusterIP
	}
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      llm.Name,
			Namespace: llm.Namespace,
			Labels:    labelsFor(llm),
		},
		Spec: corev1.ServiceSpec{
			Type:     svcType,
			Selector: selectorLabelsFor(llm),
			Ports: []corev1.ServicePort{{
				Name:       httpPortName,
				Port:       port,
				TargetPort: intstr.FromInt32(port),
				Protocol:   corev1.ProtocolTCP,
			}},
		},
	}
}

// buildHPA returns the desired HorizontalPodAutoscaler targeting the LLM's
// Deployment. Only called when autoscaling is enabled.
func buildHPA(llm *llmv1alpha1.LLM) *autoscalingv2.HorizontalPodAutoscaler {
	as := llm.Spec.Autoscaling

	minReplicas := ptr.To(int32(1))
	if as.MinReplicas != nil {
		minReplicas = as.MinReplicas
	}

	var metrics []autoscalingv2.MetricSpec
	if as.TargetCPUUtilization != nil {
		metrics = append(metrics, autoscalingv2.MetricSpec{
			Type: autoscalingv2.ResourceMetricSourceType,
			Resource: &autoscalingv2.ResourceMetricSource{
				Name: corev1.ResourceCPU,
				Target: autoscalingv2.MetricTarget{
					Type:               autoscalingv2.UtilizationMetricType,
					AverageUtilization: as.TargetCPUUtilization,
				},
			},
		})
	}
	if as.TargetGPUUtilization != nil {
		metrics = append(metrics, autoscalingv2.MetricSpec{
			Type: autoscalingv2.PodsMetricSourceType,
			Pods: &autoscalingv2.PodsMetricSource{
				Metric: autoscalingv2.MetricIdentifier{Name: gpuMetricName},
				Target: autoscalingv2.MetricTarget{
					Type:         autoscalingv2.AverageValueMetricType,
					AverageValue: resource.NewQuantity(int64(*as.TargetGPUUtilization), resource.DecimalSI),
				},
			},
		})
	}
	// Fall back to a CPU target so the HPA always has at least one metric.
	if len(metrics) == 0 {
		metrics = append(metrics, autoscalingv2.MetricSpec{
			Type: autoscalingv2.ResourceMetricSourceType,
			Resource: &autoscalingv2.ResourceMetricSource{
				Name: corev1.ResourceCPU,
				Target: autoscalingv2.MetricTarget{
					Type:               autoscalingv2.UtilizationMetricType,
					AverageUtilization: ptr.To(defaultCPUUtilization),
				},
			},
		})
	}

	return &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      llm.Name,
			Namespace: llm.Namespace,
			Labels:    labelsFor(llm),
		},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       llm.Name,
			},
			MinReplicas: minReplicas,
			MaxReplicas: as.MaxReplicas,
			Metrics:     metrics,
		},
	}
}
