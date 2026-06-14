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
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	llmv1alpha1 "github.com/youngcheor/vllm-operator/api/v1alpha1"
)

// argValue returns the value following the given flag in an argument slice.
func argValue(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// hasArg reports whether the flag is present in the argument slice.
func hasArg(args []string, flag string) bool {
	return slices.Contains(args, flag)
}

var _ = Describe("LLM Controller", func() {
	const namespace = "default"

	ctx := context.Background()

	newReconciler := func() *LLMReconciler {
		return &LLMReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
		}
	}

	reconcileOnce := func(name string) {
		_, err := newReconciler().Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: name, Namespace: namespace},
		})
		Expect(err).NotTo(HaveOccurred())
	}

	deleteLLM := func(name string) {
		llm := &llmv1alpha1.LLM{}
		key := types.NamespacedName{Name: name, Namespace: namespace}
		if err := k8sClient.Get(ctx, key, llm); err == nil {
			Expect(k8sClient.Delete(ctx, llm)).To(Succeed())
		} else {
			Expect(errors.IsNotFound(err)).To(BeTrue())
		}
		// envtest has no garbage collector, so remove owned objects explicitly.
		_ = k8sClient.Delete(ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}})
		_ = k8sClient.Delete(ctx, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}})
		_ = k8sClient.Delete(ctx, &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: name + "-cache", Namespace: namespace}})
		_ = k8sClient.Delete(ctx, &autoscalingv2.HorizontalPodAutoscaler{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}})
		_ = k8sClient.Delete(ctx, &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}})
		_ = k8sClient.Delete(ctx, &monitoringv1.ServiceMonitor{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}})
	}

	getDeployment := func(name string) *appsv1.Deployment {
		deploy := &appsv1.Deployment{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, deploy)).To(Succeed())
		return deploy
	}

	Context("When reconciling a basic LLM", func() {
		const resourceName = "test-basic"

		BeforeEach(func() {
			llm := &llmv1alpha1.LLM{
				ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: namespace},
				Spec: llmv1alpha1.LLMSpec{
					Model: "Qwen/Qwen2.5-1.5B-Instruct",
				},
			}
			Expect(k8sClient.Create(ctx, llm)).To(Succeed())
		})

		AfterEach(func() {
			deleteLLM(resourceName)
		})

		It("creates a Deployment fronted by a Service", func() {
			reconcileOnce(resourceName)

			By("creating a Deployment running vLLM")
			deploy := getDeployment(resourceName)
			Expect(deploy.Spec.Template.Spec.Containers).To(HaveLen(1))
			container := deploy.Spec.Template.Spec.Containers[0]
			Expect(container.Name).To(Equal(containerName))
			Expect(container.Image).To(Equal(defaultImage))
			Expect(*deploy.Spec.Replicas).To(Equal(int32(1)))

			By("passing the model and port to vLLM")
			Expect(argValue(container.Args, "--model")).To(Equal("Qwen/Qwen2.5-1.5B-Instruct"))
			Expect(argValue(container.Args, "--served-model-name")).To(Equal("Qwen/Qwen2.5-1.5B-Instruct"))
			Expect(argValue(container.Args, "--port")).To(Equal("8000"))

			By("configuring a /health readiness probe")
			Expect(container.ReadinessProbe).NotTo(BeNil())
			Expect(container.ReadinessProbe.HTTPGet.Path).To(Equal("/health"))

			By("setting an owner reference back to the LLM")
			Expect(deploy.OwnerReferences).To(HaveLen(1))
			Expect(deploy.OwnerReferences[0].Kind).To(Equal("LLM"))
			Expect(deploy.OwnerReferences[0].Name).To(Equal(resourceName))

			By("creating a Service on port 8000")
			svc := &corev1.Service{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: resourceName, Namespace: namespace}, svc)).To(Succeed())
			Expect(svc.Spec.Type).To(Equal(corev1.ServiceTypeClusterIP))
			Expect(svc.Spec.Ports).To(HaveLen(1))
			Expect(svc.Spec.Ports[0].Port).To(Equal(int32(8000)))

			By("reporting status with an endpoint and Progressing phase")
			llm := &llmv1alpha1.LLM{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: resourceName, Namespace: namespace}, llm)).To(Succeed())
			Expect(llm.Status.Phase).To(Equal(llmv1alpha1.LLMPhaseProgressing))
			Expect(llm.Status.Endpoint).To(Equal("http://test-basic.default.svc:8000/v1"))
			Expect(llm.Status.ObservedGeneration).To(Equal(llm.Generation))
		})
	})

	Context("When the LLM specifies engine args and a custom port", func() {
		const resourceName = "test-args"

		BeforeEach(func() {
			llm := &llmv1alpha1.LLM{
				ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: namespace},
				Spec: llmv1alpha1.LLMSpec{
					Model:           "meta-llama/Llama-3.1-8B-Instruct",
					ServedModelName: "llama3",
					Service:         llmv1alpha1.ServiceSpec{Port: 9000},
					Args: llmv1alpha1.VLLMArgs{
						TensorParallelSize:   ptr.To(int32(2)),
						DType:                "bfloat16",
						MaxModelLen:          ptr.To(int32(4096)),
						GPUMemoryUtilization: "0.9",
						TrustRemoteCode:      ptr.To(true),
					},
					ExtraArgs: []string{"--enable-prefix-caching"},
				},
			}
			Expect(k8sClient.Create(ctx, llm)).To(Succeed())
		})

		AfterEach(func() {
			deleteLLM(resourceName)
		})

		It("renders the args onto the vLLM command line", func() {
			reconcileOnce(resourceName)

			container := getDeployment(resourceName).Spec.Template.Spec.Containers[0]
			Expect(argValue(container.Args, "--served-model-name")).To(Equal("llama3"))
			Expect(argValue(container.Args, "--port")).To(Equal("9000"))
			Expect(argValue(container.Args, "--tensor-parallel-size")).To(Equal("2"))
			Expect(argValue(container.Args, "--dtype")).To(Equal("bfloat16"))
			Expect(argValue(container.Args, "--max-model-len")).To(Equal("4096"))
			Expect(argValue(container.Args, "--gpu-memory-utilization")).To(Equal("0.9"))
			Expect(hasArg(container.Args, "--trust-remote-code")).To(BeTrue())
			Expect(hasArg(container.Args, "--enable-prefix-caching")).To(BeTrue())
			Expect(container.Ports[0].ContainerPort).To(Equal(int32(9000)))
		})
	})

	Context("When the replica count changes", func() {
		const resourceName = "test-scale"

		BeforeEach(func() {
			llm := &llmv1alpha1.LLM{
				ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: namespace},
				Spec: llmv1alpha1.LLMSpec{
					Model:    "Qwen/Qwen2.5-1.5B-Instruct",
					Replicas: ptr.To(int32(1)),
				},
			}
			Expect(k8sClient.Create(ctx, llm)).To(Succeed())
		})

		AfterEach(func() {
			deleteLLM(resourceName)
		})

		It("propagates the new replica count to the Deployment", func() {
			reconcileOnce(resourceName)
			Expect(*getDeployment(resourceName).Spec.Replicas).To(Equal(int32(1)))

			By("scaling the LLM to 3 replicas")
			llm := &llmv1alpha1.LLM{}
			key := types.NamespacedName{Name: resourceName, Namespace: namespace}
			Expect(k8sClient.Get(ctx, key, llm)).To(Succeed())
			llm.Spec.Replicas = ptr.To(int32(3))
			Expect(k8sClient.Update(ctx, llm)).To(Succeed())

			reconcileOnce(resourceName)
			Expect(*getDeployment(resourceName).Spec.Replicas).To(Equal(int32(3)))
		})
	})

	Context("When model caching is enabled", func() {
		const resourceName = "test-cache"

		BeforeEach(func() {
			llm := &llmv1alpha1.LLM{
				ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: namespace},
				Spec: llmv1alpha1.LLMSpec{
					Model: "Qwen/Qwen2.5-1.5B-Instruct",
					ModelCache: &llmv1alpha1.ModelCacheSpec{
						Size: resource.MustParse("20Gi"),
					},
					HFTokenSecret: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "hf-token"},
						Key:                  "token",
					},
				},
			}
			Expect(k8sClient.Create(ctx, llm)).To(Succeed())
		})

		AfterEach(func() {
			deleteLLM(resourceName)
		})

		It("creates a PVC and mounts it into the vLLM container", func() {
			reconcileOnce(resourceName)

			By("creating a cache PVC of the requested size")
			pvc := &corev1.PersistentVolumeClaim{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: resourceName + "-cache", Namespace: namespace}, pvc)).To(Succeed())
			Expect(pvc.Spec.AccessModes).To(ContainElement(corev1.ReadWriteOnce))
			Expect(pvc.Spec.Resources.Requests.Storage().String()).To(Equal("20Gi"))
			Expect(pvc.OwnerReferences).To(HaveLen(1))
			Expect(pvc.OwnerReferences[0].Name).To(Equal(resourceName))

			By("mounting the PVC at the HuggingFace cache path")
			podSpec := getDeployment(resourceName).Spec.Template.Spec
			Expect(podSpec.Volumes).To(HaveLen(1))
			Expect(podSpec.Volumes[0].PersistentVolumeClaim.ClaimName).To(Equal(resourceName + "-cache"))
			container := podSpec.Containers[0]
			Expect(container.VolumeMounts).To(HaveLen(1))
			Expect(container.VolumeMounts[0].MountPath).To(Equal("/root/.cache/huggingface"))

			By("injecting the HF token and HF_HOME env")
			var hasToken, hasHFHome bool
			for _, e := range container.Env {
				if e.Name == "HUGGING_FACE_HUB_TOKEN" && e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil {
					hasToken = e.ValueFrom.SecretKeyRef.Name == "hf-token"
				}
				if e.Name == "HF_HOME" && e.Value == "/root/.cache/huggingface" {
					hasHFHome = true
				}
			}
			Expect(hasToken).To(BeTrue(), "expected HUGGING_FACE_HUB_TOKEN from secret hf-token")
			Expect(hasHFHome).To(BeTrue(), "expected HF_HOME pointing at the cache mount")
		})
	})

	Context("When model caching is disabled", func() {
		const resourceName = "test-nocache"

		BeforeEach(func() {
			llm := &llmv1alpha1.LLM{
				ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: namespace},
				Spec:       llmv1alpha1.LLMSpec{Model: "Qwen/Qwen2.5-1.5B-Instruct"},
			}
			Expect(k8sClient.Create(ctx, llm)).To(Succeed())
		})

		AfterEach(func() {
			deleteLLM(resourceName)
		})

		It("does not create a PVC or cache volume", func() {
			reconcileOnce(resourceName)

			pvc := &corev1.PersistentVolumeClaim{}
			err := k8sClient.Get(ctx, types.NamespacedName{Name: resourceName + "-cache", Namespace: namespace}, pvc)
			Expect(errors.IsNotFound(err)).To(BeTrue())

			Expect(getDeployment(resourceName).Spec.Template.Spec.Volumes).To(BeEmpty())
		})
	})

	getHPA := func(name string) (*autoscalingv2.HorizontalPodAutoscaler, error) {
		hpa := &autoscalingv2.HorizontalPodAutoscaler{}
		err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, hpa)
		return hpa, err
	}

	Context("When autoscaling is enabled", func() {
		const resourceName = "test-hpa"

		BeforeEach(func() {
			llm := &llmv1alpha1.LLM{
				ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: namespace},
				Spec: llmv1alpha1.LLMSpec{
					Model: "Qwen/Qwen2.5-1.5B-Instruct",
					Autoscaling: &llmv1alpha1.AutoscalingSpec{
						MinReplicas:          ptr.To(int32(2)),
						MaxReplicas:          5,
						TargetCPUUtilization: ptr.To(int32(70)),
						TargetGPUUtilization: ptr.To(int32(80)),
					},
				},
			}
			Expect(k8sClient.Create(ctx, llm)).To(Succeed())
		})

		AfterEach(func() {
			deleteLLM(resourceName)
		})

		It("creates an HPA targeting the Deployment and leaves replicas unmanaged", func() {
			reconcileOnce(resourceName)

			By("creating an HPA with the configured bounds")
			hpa, err := getHPA(resourceName)
			Expect(err).NotTo(HaveOccurred())
			Expect(*hpa.Spec.MinReplicas).To(Equal(int32(2)))
			Expect(hpa.Spec.MaxReplicas).To(Equal(int32(5)))
			Expect(hpa.Spec.ScaleTargetRef.Kind).To(Equal("Deployment"))
			Expect(hpa.Spec.ScaleTargetRef.Name).To(Equal(resourceName))
			Expect(hpa.OwnerReferences).To(HaveLen(1))
			Expect(hpa.OwnerReferences[0].Name).To(Equal(resourceName))

			By("configuring both CPU and GPU metrics")
			var hasCPU, hasGPU bool
			for _, m := range hpa.Spec.Metrics {
				if m.Type == autoscalingv2.ResourceMetricSourceType && m.Resource.Name == corev1.ResourceCPU {
					hasCPU = *m.Resource.Target.AverageUtilization == int32(70)
				}
				if m.Type == autoscalingv2.PodsMetricSourceType {
					hasGPU = m.Pods.Metric.Name == "DCGM_FI_DEV_GPU_UTIL"
				}
			}
			Expect(hasCPU).To(BeTrue(), "expected CPU utilization metric at 70%")
			Expect(hasGPU).To(BeTrue(), "expected GPU pods metric")

			By("not overwriting replicas scaled by the HPA on subsequent reconciles")
			deploy := getDeployment(resourceName)
			deploy.Spec.Replicas = ptr.To(int32(4)) // simulate the HPA scaling up
			Expect(k8sClient.Update(ctx, deploy)).To(Succeed())

			reconcileOnce(resourceName)
			Expect(*getDeployment(resourceName).Spec.Replicas).To(Equal(int32(4)))
		})
	})

	Context("When autoscaling is toggled off", func() {
		const resourceName = "test-hpa-off"

		BeforeEach(func() {
			llm := &llmv1alpha1.LLM{
				ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: namespace},
				Spec: llmv1alpha1.LLMSpec{
					Model: "Qwen/Qwen2.5-1.5B-Instruct",
					Autoscaling: &llmv1alpha1.AutoscalingSpec{
						MaxReplicas: 3,
					},
				},
			}
			Expect(k8sClient.Create(ctx, llm)).To(Succeed())
		})

		AfterEach(func() {
			deleteLLM(resourceName)
		})

		It("deletes the HPA when autoscaling is removed", func() {
			reconcileOnce(resourceName)
			_, err := getHPA(resourceName)
			Expect(err).NotTo(HaveOccurred())

			By("removing the autoscaling spec")
			llm := &llmv1alpha1.LLM{}
			key := types.NamespacedName{Name: resourceName, Namespace: namespace}
			Expect(k8sClient.Get(ctx, key, llm)).To(Succeed())
			llm.Spec.Autoscaling = nil
			Expect(k8sClient.Update(ctx, llm)).To(Succeed())

			reconcileOnce(resourceName)
			_, err = getHPA(resourceName)
			Expect(errors.IsNotFound(err)).To(BeTrue())
		})
	})

	Context("When an Ingress is configured", func() {
		const resourceName = "test-ingress"

		BeforeEach(func() {
			llm := &llmv1alpha1.LLM{
				ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: namespace},
				Spec: llmv1alpha1.LLMSpec{
					Model: "Qwen/Qwen2.5-1.5B-Instruct",
					Ingress: &llmv1alpha1.IngressSpec{
						Host:          "qwen.example.com",
						ClassName:     ptr.To("nginx"),
						TLSSecretName: "qwen-tls",
					},
				},
			}
			Expect(k8sClient.Create(ctx, llm)).To(Succeed())
		})

		AfterEach(func() {
			deleteLLM(resourceName)
		})

		getIngress := func() (*networkingv1.Ingress, error) {
			ing := &networkingv1.Ingress{}
			err := k8sClient.Get(ctx, types.NamespacedName{Name: resourceName, Namespace: namespace}, ing)
			return ing, err
		}

		It("creates an Ingress routing to the Service and cleans it up when removed", func() {
			reconcileOnce(resourceName)

			ing, err := getIngress()
			Expect(err).NotTo(HaveOccurred())
			Expect(*ing.Spec.IngressClassName).To(Equal("nginx"))
			Expect(ing.Spec.Rules).To(HaveLen(1))
			rule := ing.Spec.Rules[0]
			Expect(rule.Host).To(Equal("qwen.example.com"))
			backend := rule.HTTP.Paths[0].Backend.Service
			Expect(backend.Name).To(Equal(resourceName))
			Expect(backend.Port.Number).To(Equal(int32(8000)))
			Expect(ing.Spec.TLS).To(HaveLen(1))
			Expect(ing.Spec.TLS[0].SecretName).To(Equal("qwen-tls"))
			Expect(ing.OwnerReferences).To(HaveLen(1))

			By("removing the ingress spec")
			llm := &llmv1alpha1.LLM{}
			key := types.NamespacedName{Name: resourceName, Namespace: namespace}
			Expect(k8sClient.Get(ctx, key, llm)).To(Succeed())
			llm.Spec.Ingress = nil
			Expect(k8sClient.Update(ctx, llm)).To(Succeed())

			reconcileOnce(resourceName)
			_, err = getIngress()
			Expect(errors.IsNotFound(err)).To(BeTrue())
		})
	})

	Context("When monitoring is enabled", func() {
		const resourceName = "test-monitor"

		BeforeEach(func() {
			llm := &llmv1alpha1.LLM{
				ObjectMeta: metav1.ObjectMeta{Name: resourceName, Namespace: namespace},
				Spec: llmv1alpha1.LLMSpec{
					Model: "Qwen/Qwen2.5-1.5B-Instruct",
					Monitoring: &llmv1alpha1.MonitoringSpec{
						ServiceMonitor: true,
						Interval:       "15s",
						Labels:         map[string]string{"release": "kube-prometheus-stack"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, llm)).To(Succeed())
		})

		AfterEach(func() {
			deleteLLM(resourceName)
		})

		getSM := func() (*monitoringv1.ServiceMonitor, error) {
			sm := &monitoringv1.ServiceMonitor{}
			err := k8sClient.Get(ctx, types.NamespacedName{Name: resourceName, Namespace: namespace}, sm)
			return sm, err
		}

		It("creates a ServiceMonitor scraping /metrics and cleans it up when disabled", func() {
			reconcileOnce(resourceName)

			sm, err := getSM()
			Expect(err).NotTo(HaveOccurred())
			Expect(sm.Labels).To(HaveKeyWithValue("release", "kube-prometheus-stack"))
			Expect(sm.Spec.Endpoints).To(HaveLen(1))
			Expect(sm.Spec.Endpoints[0].Port).To(Equal("http"))
			Expect(sm.Spec.Endpoints[0].Path).To(Equal("/metrics"))
			Expect(sm.Spec.Endpoints[0].Interval).To(Equal(monitoringv1.Duration("15s")))
			Expect(sm.OwnerReferences).To(HaveLen(1))

			By("disabling monitoring")
			llm := &llmv1alpha1.LLM{}
			key := types.NamespacedName{Name: resourceName, Namespace: namespace}
			Expect(k8sClient.Get(ctx, key, llm)).To(Succeed())
			llm.Spec.Monitoring = nil
			Expect(k8sClient.Update(ctx, llm)).To(Succeed())

			reconcileOnce(resourceName)
			_, err = getSM()
			Expect(errors.IsNotFound(err)).To(BeTrue())
		})
	})
})
