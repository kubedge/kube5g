# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

Kube5G (Mosaic5G / EURECOM) packages OpenAirInterface (OAI) 4G/5G components as snaps running inside systemd-enabled Docker containers, and deploys them via docker-compose, plain Kubernetes manifests, or a Kubernetes operator. This GitHub repo (`kubedge/kube5g`) mirrors `https://gitlab.eurecom.fr/mosaic5g/kube5g` (git remote `upstream`).

There is no Makefile, no CI config, no linter config, and no tests (outside `openshift/m5g-operator/vendor/`). Several compiled binaries are committed (`dockers/docker-hook/cmd/hook/hook`, `dockers/docker-build/build/hook`, `openshift/m5g-operator/build/_output/bin/m5g-operator`) — rebuild them when changing the corresponding source.

## How the pieces fit together

1. **`dockers/docker-hook`** (Go, GOPATH-style, no go.mod; imports `docker-hook/internal/...`): `cmd/hook` is the in-container init. It reads `/root/config/conf.yaml`, runs `snap install <oai-cn|oai-ran|flexran|ll-mec> --channel=edge --devmode`, rewrites the snap's config files with `sed -i` (`internal/oai/*.go`), then starts the snap services. Flags: `-installCN`, `-installRAN`, `-installHSS`, `-installMME`, `-installSPGW`, `-installFlexRAN`, `-installMEC`. Logs to `/root/hook.log`.
2. **`dockers/docker-build`**: per-component Dockerfiles (`oai-cn`, `oai-ran`, `flexran`, `ll-mec`) based on `ubuntu:16.04` with snapd, `CMD ["/sbin/init"]`. Boot chain: systemd → `/etc/rc.local` → `/root/init.sh` → `/root/hook -installX`. `build/build.sh` builds a base image, runs it privileged until the snap is installed, then `docker commit`s the result — so published images already contain the installed snaps.
3. **Consumers** of the `mosaic5gecosys/{oaicn,oairan,flexran,llmec}:<tag>` images:
   - `dockers/docker-compose/{lte,lte-with-flexran}` — bind-mounts a local `conf.yaml` into the container.
   - `kubernetes/lte` — plain manifests (mysql, oaicn, oairan); no ConfigMap, relies on the conf.yaml baked into the image.
   - `openshift/m5g-operator` — renders the CR spec into ConfigMap `mosaic5g-config` (key `conf.yaml`) mounted at `/root/config`.

**Config contract:** the YAML keys in `conf.yaml`, the operator CR spec (`openshift/m5g-operator/pkg/apis/mosaic5g/v1alpha1/mosaic5g_types.go`), and the hook's config struct (`dockers/docker-hook/internal/pkg/common/cfg.go`) must stay in sync. Keys include `mcc`, `mnc`, `eutraBand`, `downlinkFrequency`, `uplinkFrequencyOffset`, `flexRAN`, `configurationPathofCN/RAN`, `snapBinaryPath`, `dns`, `*DomainName`; the hook additionally reads `NumberRbDl`, `MaxRxGain`, `ParallelConfig`, which the CR doesn't set.

**Fragility:** the hook's sed edits target hardcoded line numbers in the OAI snap config templates (e.g. `enb.band7.tm1.50PRB.usrpb210.conf`, `mme.conf`, `spgw.conf`). If the snap templates change, those edits silently break.

## m5g-operator (`openshift/m5g-operator`)

- Built with operator-sdk using `dep` (`Gopkg.toml` pins operator-sdk `v0.7.0`, controller-runtime `v0.1.10`, k8s 1.13) and a committed `vendor/`. The code uses the v0.7-era API, even though the README says v0.15.2. Module path `github.com/tig4605246/m5g-operator` — the project must be placed at that path in GOPATH to build.
- CRD: group `mosaic5g.com`, version `v1alpha1`, kind `Mosaic5g`. After editing types, run `operator-sdk generate k8s` to regenerate `zz_generated.*.go`. Sample CR: `deploy/crds/mosaic5g_v1alpha1_mosaic5g_cr.yaml`.
- The controller (`pkg/controller/mosaic5g/mosaic5g_controller.go`) creates resources in this order, each step waiting on the previous one being ready: ConfigMap `mosaic5g-config` → `mysql:5.6` Deployment + Service → CN Deployment `<cr>-<mmeDomainName>` (image `cnImage`, replicas = `spec.size`) + headless Service `oaicn` → RAN Deployment `<cr>-oairan` (image `ranImage`, mounts `/dev/bus/usb/` for the USRP). Pods are privileged, mount `/sys/fs/cgroup` and `/lib/modules`, and prefer nodes labelled `oai=cn` / `oai=ran` (`internal/util/util.go`).
- If the CR spec changes, the controller updates the ConfigMap and **deletes** the CN and RAN Deployments so they are recreated. It does not deploy FlexRAN; `flexRAN` is only passed to the eNB config.

Commands (`m5goperator.sh` hardcodes `KUBECONFIG`, so edit it first):
```bash
./m5goperator.sh init                 # apply CRD
./m5goperator.sh local                # operator-sdk up local --namespace=default
./m5goperator.sh container start|stop # deploy/remove operator pod (deploy/*.yaml, image tig4605246/m5g_operator:0.1)
./m5goperator.sh clean                # remove CRD
./m5goperator.sh from_clean_machine   # install microk8s 1.14 + kubectl, enable privileged
kubectl apply -f deploy/crds/mosaic5g_v1alpha1_mosaic5g_cr.yaml
```
`api.sh` drives the CR through curl against the API server (`init`, `apply_cr`, `delete_cr`, `patch_11`, `patch_12` to switch the image tags between 1.1 and 1.2).

## Building images (`dockers/docker-build/build`)

```bash
./build.sh oai-cn <tag>     # -> mosaic5gecosys/oaicn:<tag>; also oai-ran, flexran, ll-mec
./build.sh build-hook       # go build the hook from GOPATH (expects ${GOPATH}/src/oai-snap-in-docker/cmd/hook)
./build.sh clean-all        # READMEs say clean_all, but the script uses clean-all
```
To change the registry or base tag, edit `REPO_NAME`, `TAG_BASE` and `BASE_CONTAINER` at the top of the script. Requirements: Ubuntu 16.04/18.04 host, Docker usable without root, Go only to rebuild the hook. Containers run privileged with snap confinement disabled.

## Running

- docker-compose: edit `conf.yaml` in `dockers/docker-compose/lte`, then `docker-compose up -d`.
- Kubernetes: microk8s with dns enabled and `--allow-privileged=true` on the kubelet and apiserver; `kubectl apply -f kubernetes/lte/`. Tested on microk8s 1.14 with a USRP B210 for the RAN.

## Known inconsistencies

- The `flexran` and `ll-mec` Dockerfiles and `lte-with-flexran/docker-compose.yml` place conf.yaml at `/root/conf.yaml`, but the hook only reads `/root/config/conf.yaml`.
- On CR deletion, the operator deletes the ConfigMap with the namespace hardcoded to `default`.
- `kubernetes/lte/README.md` apply commands refer to an old repo layout.
