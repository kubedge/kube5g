# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

Kube5G (Mosaic5G / EURECOM) packages OpenAirInterface (OAI) 4G/5G components as snaps running inside systemd-enabled Docker containers, and deploys them via docker-compose, plain Kubernetes manifests, or a Kubernetes operator. This GitHub repo (`kubedge/kube5g`) mirrors `https://gitlab.eurecom.fr/mosaic5g/kube5g` (git remote `upstream`).

The two Go modules (`openshift/m5g-operator`, `dockers/docker-hook`) target Go 1.27 and each has its own `go.mod` and Makefile. CI lives in the repo-root `.github/workflows/` (`m5g-operator.yml`, `docker-hook.yml`). `dockers/docker-build/build/hook` is a committed static linux/amd64 binary baked into the images. Rebuild it after changing docker-hook with `make -C dockers/docker-hook build`.

## How the pieces fit together

1. **`dockers/docker-hook`** (Go module `github.com/kubedge/kube5g/dockers/docker-hook`): `cmd/hook` is the in-container init. It reads `/root/config/conf.yaml`, runs `snap install <oai-cn|oai-ran|flexran|ll-mec> --channel=edge --devmode`, rewrites the snap's config files with `sed -i` (`internal/oai/*.go`), then starts the snap services. Flags: `-installCN`, `-installRAN`, `-installHSS`, `-installMME`, `-installSPGW`, `-installFlexRAN`, `-installMEC`. Logs to `/root/hook.log`.
2. **`dockers/docker-build`**: per-component Dockerfiles (`oai-cn`, `oai-ran`, `flexran`, `ll-mec`) based on `ubuntu:16.04` with snapd, `CMD ["/sbin/init"]`. Boot chain: systemd → `/etc/rc.local` → `/root/init.sh` → `/root/hook -installX`. `build/build.sh` builds a base image, runs it privileged until the snap is installed, then `docker commit`s the result — so published images already contain the installed snaps.
3. **Consumers** of the `mosaic5gecosys/{oaicn,oairan,flexran,llmec}:<tag>` images:
   - `dockers/docker-compose/{lte,lte-with-flexran}` — bind-mounts a local `conf.yaml` into the container.
   - `kubernetes/lte` — plain manifests (mysql, oaicn, oairan); no ConfigMap, relies on the conf.yaml baked into the image.
   - `openshift/m5g-operator`: renders the CR spec into ConfigMap `mosaic5g-config` (key `conf.yaml`) mounted at `/root/config`.

**Config contract:** the YAML keys in `conf.yaml`, the operator CR spec's `json` tags (`openshift/m5g-operator/api/v1alpha1/mosaic5g_types.go`), and the hook's config struct (`dockers/docker-hook/internal/pkg/common/cfg.go`) must stay in sync. `cfg_test.go` in the hook holds a fixture of the operator's rendered conf.yaml. The hook parses non-strictly, so operator-only keys (`size`, `cnImage`, `mysqlImage`, ...) are ignored. Keys include `mcc`, `mnc`, `eutraBand`, `downlinkFrequency`, `uplinkFrequencyOffset`, `flexRAN`, `configurationPathofCN/RAN`, `snapBinaryPath`, `dns`, `*DomainName`; the hook additionally reads `NumberRbDl`, `MaxRxGain`, `ParallelConfig`, which the CR doesn't set.

**Fragility:** the hook's sed edits target hardcoded line numbers in the OAI snap config templates (e.g. `enb.band7.tm1.50PRB.usrpb210.conf`, `mme.conf`, `spgw.conf`). If the snap templates change, those edits silently break.

## m5g-operator (`openshift/m5g-operator`)

Kubebuilder v4 project (controller-runtime v0.25, k8s 0.37), module `github.com/kubedge/kube5g/openshift/m5g-operator`. CRD: group `mosaic5g.com`, version `v1alpha1`, kind `Mosaic5g`. The API is unchanged from the original operator-sdk v0.7 version. `AGENTS.md` in that directory is Kubebuilder's own guidance: don't edit generated files (`zz_generated.*`, `config/crd/bases`, `config/rbac/role.yaml`, `PROJECT`), and use `kubebuilder create ...` for new APIs.

```bash
cd openshift/m5g-operator
make build          # manifests + generate + fmt + vet + build
make test           # envtest unit tests
go test ./internal/controller/ -ginkgo.focus "rolls the pods"   # single spec; run make test once first so bin/k8s has envtest binaries
make lint           # golangci-lint v2.15 (older versions can't read Go 1.27 export data)
make test-e2e       # Kind-based
make run            # run against current kubeconfig
make crc-deploy crc-sample   # OpenShift Local: push to CRC internal registry, deploy, apply sample in ns "mosaic5g"
```

Controller (`internal/controller/mosaic5g_controller.go`, object builders in `resources.go`):
- Creates objects in order, each step waiting (via `RequeueAfter`, not errors) on the previous Deployment having `ReadyReplicas > 0`: ServiceAccount `mosaic5g` (+ RoleBinding to `system:openshift:scc:privileged` when OpenShift is detected in `cmd/main.go`) → ConfigMap → `mysql` Deployment/Service → oai-cn Deployment `<cr>-<mmeDomainName>` + headless Service `oaicn` → oai-ran Deployment `<cr>-oairan`.
- Uses `controllerutil.CreateOrUpdate`. `mutateDeployment` merges into the existing pod spec so API-server defaults don't cause update churn. The "is a no-op when nothing changed" test guards this.
- Spec changes roll out through the `mosaic5g.com/config-hash` pod-template annotation with the `Recreate` strategy. Deployment selectors keep the legacy `app: oai|oaicn|mosaic5g` labels, because selectors are immutable.
- Fixed object names mean one `Mosaic5g` per namespace.
- Defaulted spec fields need `omitempty`, or Go clients send `""` and the CRD default never applies. `size` is `*int32` so that `0` remains valid.

## Building images (`dockers/docker-build/build`)

```bash
./build.sh oai-cn <tag>     # -> mosaic5gecosys/oaicn:<tag>; also oai-ran, flexran, ll-mec
./build.sh build-hook       # static linux/amd64 build of ../../docker-hook/cmd/hook into ./hook
./build.sh clean-all        # READMEs say clean_all, but the script uses clean-all
```
To change the registry or base tag, edit `REPO_NAME`, `TAG_BASE` and `BASE_CONTAINER` at the top of the script. Requirements: Ubuntu 16.04/18.04 host, Docker usable without root, Go only to rebuild the hook. The images (Ubuntu 16.04, systemd 229, x86-64) are not modernized; they need x86-64 nodes, and they predate cgroup v2. Containers run privileged with snap confinement disabled.

## Running

- docker-compose: edit `conf.yaml` in `dockers/docker-compose/lte`, then `docker-compose up -d`.
- Kubernetes: microk8s with dns enabled and `--allow-privileged=true` on the kubelet and apiserver; `kubectl apply -f kubernetes/lte/`. Tested on microk8s 1.14 with a USRP B210 for the RAN.

## Known inconsistencies

- The `flexran` and `ll-mec` Dockerfiles and `lte-with-flexran/docker-compose.yml` place conf.yaml at `/root/conf.yaml`, but the hook only reads `/root/config/conf.yaml`.
- `kubernetes/lte/README.md` apply commands refer to an old repo layout.
