# m5g-operator

Kubernetes / OpenShift operator that deploys a Mosaic5G OAI LTE network
(MySQL, oai-cn and oai-ran) from a single `Mosaic5g` custom resource.

Built with [Kubebuilder](https://book.kubebuilder.io) v4 and
controller-runtime. It replaces the original operator-sdk v0.7 / `dep`
implementation; the API (`mosaic5g.com/v1alpha1`, kind `Mosaic5g`) and its
fields are unchanged, so existing CRs keep working.

## How it works

For each `Mosaic5g` the controller creates, in its namespace:

1. a `mosaic5g` ServiceAccount used by all pods (on OpenShift it is bound to
   the `privileged` SCC through a RoleBinding to
   `system:openshift:scc:privileged`);
2. the `mosaic5g-config` ConfigMap holding the spec rendered as `conf.yaml`,
   mounted at `/root/config` where docker-hook reads it;
3. the `mysql` Deployment and headless Service;
4. once MySQL is ready, the `<name>-<mmeDomainName>` oai-cn Deployment and the
   headless `oaicn` Service;
5. once oai-cn is ready, the `<name>-oairan` Deployment.

Changing the spec updates the ConfigMap and the `mosaic5g.com/config-hash`
annotation on the pod templates, which recreates the pods (Deployments use the
`Recreate` strategy). `status.conditions[Available]` reports progress and
`status.nodes` lists the pod names.

Object names are fixed (they are referenced by the `*DomainName` fields), so
only one `Mosaic5g` is supported per namespace.

## Requirements

- Go 1.27+
- Docker or Podman
- kubectl, or `oc` for OpenShift
- A Kubernetes or OpenShift cluster

The Makefile downloads controller-gen, kustomize, setup-envtest and
golangci-lint into `bin/` on first use.

## Development

```sh
make build        # regenerate CRD/RBAC/deepcopy, fmt, vet, build bin/manager
make test         # unit tests against envtest (a local kube-apiserver + etcd)
make lint         # golangci-lint
make test-e2e     # e2e tests on a throw-away Kind cluster
make run          # run the controller locally against the current kubeconfig
make install      # install the CRD only
```

After editing `api/v1alpha1/mosaic5g_types.go` run `make manifests generate`.
The `json` names of the spec fields are the `conf.yaml` keys read by
`dockers/docker-hook/internal/pkg/common/cfg.go`; keep both in sync.

## OpenShift Local (CRC)

```sh
crc start
eval $(crc oc-env)
oc login -u kubeadmin https://api.crc.testing:6443

make crc-deploy     # build, push to the CRC internal registry, deploy the operator
make crc-sample     # create the "mosaic5g" namespace and apply the sample CR
oc get mosaic5g -n mosaic5g -w
make crc-undeploy
```

`crc-deploy` pushes to `default-route-openshift-image-registry.apps-crc.testing`.
With Podman, TLS verification is disabled automatically
(`CRC_PUSH_FLAGS`); with Docker add that host to `insecure-registries` in the
Docker daemon configuration. Override `CONTAINER_TOOL`, `CRC_OPERATOR_NAMESPACE`,
`CRC_DEMO_NAMESPACE` or `CRC_TAG` as needed.

Alternatively, run the controller from your machine against CRC with
`make install run`.

## Other clusters

```sh
make docker-build docker-push IMG=<registry>/m5g-operator:<tag>
make deploy IMG=<registry>/m5g-operator:<tag>
kubectl create namespace mosaic5g
kubectl label namespace mosaic5g pod-security.kubernetes.io/enforce=privileged
kubectl apply -n mosaic5g -f config/samples/mosaic5g_v1alpha1_mosaic5g.yaml
```

`make build-installer IMG=...` writes a single `dist/install.yaml`.

## Operand limitations

The operator only manages Kubernetes objects; the OAI images
(`mosaic5gecosys/oaicn`, `oairan`) are unchanged:

- They are Ubuntu 16.04 x86-64 images running systemd and snapd as PID 1, so
  they need x86-64 nodes. OpenShift Local on Apple Silicon is arm64 and cannot
  run them.
- systemd 229 in those images predates cgroup v2, which current OpenShift and
  most current Kubernetes distributions use by default.
- oai-ran mounts `/dev/bus/usb/` for a USRP B210; on nodes without that
  directory the pod stays in ContainerCreating with a mount error.
- `mysql:5.6` is x86-64 only; set `spec.mysqlImage` to use another image.
