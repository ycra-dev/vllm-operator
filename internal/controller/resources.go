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
	corev1 "k8s.io/api/core/v1"
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

	return corev1.PodSpec{
		Containers:       []corev1.Container{container},
		NodeSelector:     llm.Spec.NodeSelector,
		Tolerations:      llm.Spec.Tolerations,
		Affinity:         llm.Spec.Affinity,
		ImagePullSecrets: llm.Spec.ImagePullSecrets,
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
