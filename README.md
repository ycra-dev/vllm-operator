# vllm-operator

A Kubernetes operator that runs [vLLM](https://github.com/vllm-project/vllm)
OpenAI-compatible inference servers from a single custom resource. Apply one
`LLM` object and the operator reconciles a Deployment, Service, model-cache PVC,
HorizontalPodAutoscaler, Ingress, and Prometheus ServiceMonitor for you.

```yaml
apiVersion: llm.vllm-operator.io/v1alpha1
kind: LLM
metadata:
  name: qwen
spec:
  model: Qwen/Qwen2.5-1.5B-Instruct
  resources:
    limits:
      nvidia.com/gpu: "1"
  args:
    maxModelLen: 4096
    gpuMemoryUtilization: "0.9"
```

```console
$ kubectl get llm
NAME   MODEL                         PHASE   AVAILABLE   AGE
qwen   Qwen/Qwen2.5-1.5B-Instruct    Ready   1           2m
```

## What it manages

| Spec field      | Reconciled object                         |
| --------------- | ----------------------------------------- |
| (always)        | `Deployment` running the vLLM server      |
| (always)        | `Service` exposing the OpenAI API         |
| `modelCache`    | `PersistentVolumeClaim` for model weights |
| `autoscaling`   | `HorizontalPodAutoscaler`                 |
| `ingress`       | `Ingress`                                 |
| `monitoring`    | Prometheus `ServiceMonitor`               |

All child objects carry owner references, so deleting the `LLM` cleans them up.

## Requirements

- A Kubernetes cluster (v1.30+ recommended).
- **GPU nodes**: the NVIDIA driver, the NVIDIA container runtime, and the
  [k8s device plugin](https://github.com/NVIDIA/k8s-device-plugin) so nodes
  advertise `nvidia.com/gpu`. Verify with:
  ```console
  $ kubectl get nodes -o jsonpath='{.items[*].status.capacity.nvidia\.com/gpu}'
  ```
- Optional: the [Prometheus Operator](https://github.com/prometheus-operator/prometheus-operator)
  CRDs if you use `monitoring.serviceMonitor` (the operator skips it gracefully
  when absent).

## Install

Install the CRDs and the controller into the cluster:

```sh
make install                      # CRDs only
make deploy IMG=<registry>/vllm-operator:tag
```

Or run the controller locally against your current kubeconfig (useful for
development):

```sh
make install
make run
```

Then apply a sample:

```sh
kubectl apply -k config/samples/
```

## `LLM` spec reference

| Field                          | Type     | Default                      | Description |
| ------------------------------ | -------- | ---------------------------- | ----------- |
| `model`                        | string   | —                            | HuggingFace model ID or local path (required). |
| `servedModelName`              | string   | `model`                      | Name exposed via the OpenAI API. |
| `image`                        | string   | `vllm/vllm-openai:latest`    | vLLM server image. |
| `replicas`                     | int      | `1`                          | Fixed replicas (ignored when `autoscaling` is set). |
| `resources`                    | object   | —                            | Container resources; request GPUs via `nvidia.com/gpu`. |
| `args.tensorParallelSize`      | int      | —                            | `--tensor-parallel-size` (GPUs per replica). |
| `args.dtype`                   | enum     | —                            | `--dtype` (auto/half/float16/bfloat16/float32). |
| `args.maxModelLen`             | int      | —                            | `--max-model-len` (context length). |
| `args.gpuMemoryUtilization`    | string   | —                            | `--gpu-memory-utilization` (0–1, e.g. `"0.9"`). |
| `args.quantization`            | string   | —                            | `--quantization` (awq/gptq/fp8/...). |
| `args.maxNumSeqs`              | int      | —                            | `--max-num-seqs`. |
| `args.trustRemoteCode`         | bool     | —                            | `--trust-remote-code`. |
| `extraArgs`                    | []string | —                            | Raw flags appended to the server command. |
| `env`                          | []EnvVar | —                            | Extra container environment variables. |
| `hfTokenSecret`                | SecretKeyRef | —                        | Secret key holding a HuggingFace token (gated models). |
| `modelCache.size`              | quantity | `50Gi`                       | Cache PVC size. |
| `modelCache.storageClassName`  | string   | cluster default              | Cache PVC StorageClass. |
| `modelCache.mountPath`         | string   | `/root/.cache/huggingface`   | In-container cache path. |
| `autoscaling.minReplicas`      | int      | `1`                          | HPA lower bound. |
| `autoscaling.maxReplicas`      | int      | —                            | HPA upper bound (required when autoscaling). |
| `autoscaling.targetCPUUtilization` | int  | `80` (fallback)              | Target CPU utilization %. |
| `autoscaling.targetGPUUtilization` | int  | —                            | Target GPU utilization % (needs DCGM exporter). |
| `service.port`                 | int      | `8000`                       | Service/container port. |
| `service.type`                 | enum     | `ClusterIP`                  | ClusterIP/NodePort/LoadBalancer. |
| `ingress.host`                 | string   | —                            | Ingress hostname (required for ingress). |
| `ingress.className`            | string   | —                            | IngressClass name. |
| `ingress.path`                 | string   | `/`                          | HTTP path prefix. |
| `ingress.tlsSecretName`        | string   | —                            | Enables TLS using the named secret. |
| `monitoring.serviceMonitor`    | bool     | `false`                      | Create a Prometheus ServiceMonitor. |
| `monitoring.interval`          | string   | `30s`                        | Scrape interval. |
| `nodeSelector` / `tolerations` / `affinity` | object | —              | Standard pod scheduling controls. |

### Status

`status` reports `phase` (Pending/Progressing/Ready/Degraded), `replicas`,
`availableReplicas`, the in-cluster `endpoint`
(`http://<name>.<namespace>.svc:<port>/v1`), and standard `conditions`
(`Available`, `Progressing`, `Degraded`).

## Notes for small GPUs

VRAM bounds what you can serve. On a ~12GB card (e.g. RTX 4070), stick to small
models or quantize larger ones:

- `Qwen/Qwen2.5-1.5B-Instruct`, `Qwen/Qwen2.5-3B-Instruct`, `TinyLlama/TinyLlama-1.1B-Chat-v1.0`
- A 7B model with `args.quantization: awq` and a modest `maxModelLen`.

## Development

```sh
make manifests generate   # regenerate CRDs/RBAC and DeepCopy after editing types
make test                 # run envtest-based unit tests
make lint                 # golangci-lint
```

See `AGENTS.md` for the project layout and contribution conventions.

## License

Apache 2.0 — see [LICENSE](LICENSE).
