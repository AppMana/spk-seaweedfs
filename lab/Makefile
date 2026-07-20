# Lab orchestrator. Bring-up order matters; each target is idempotent.
#
#   make fabric-up      network fabric + containernet topology
#   make registry-up    lab registry + fork image (built from ~/Documents/forks-seaweedfs)
#   make cluster-up     kind swlab + seaweedfs-operator + RBAC/token
#   make seaweed-fork   apply the fork-image Seaweed CR
#   make seaweed-upstream  switch the CR to the upstream image
#   make vm-up           boot the xpenology DSM VM (fetches the RR loader if needed)
#   make dsm-storage     create the storage pool/volume/seaweed dir (needs DSM_USER/DSM_PASS)
#   make provision-fork / provision-upstream / provision-bundled   (needs DSM_USER/DSM_PASS)
#   make verify-fork / verify-upstream / verify-bundled
#   make down           tear down VM, cluster, topology, fabric (registry + images stay)

SHELL := /bin/bash
DIR   := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))
KIND  ?= $(HOME)/go/bin/kind

.PHONY: fabric-up registry-up cluster-up seaweed-fork seaweed-upstream vm-up \
        provision-fork provision-upstream provision-bundled \
        verify-fork verify-upstream verify-bundled down

fabric-up:
	sudo $(DIR)net/fabric-up.sh
	sudo bash -c 'cd $(DIR)net && nohup python3 topology.py > run/topology.log 2>&1 &'
	sleep 15 && brctl show s1

registry-up:
	$(DIR)registry/registry-up.sh

cluster-up:
	$(DIR)kind/cluster-up.sh

seaweed-fork:
	kubectl --context kind-swlab apply -f $(DIR)kind/seaweed-fork.yaml

seaweed-upstream:
	kubectl --context kind-swlab apply -f $(DIR)kind/seaweed-upstream.yaml

vm-up:
	$(DIR)dsm/fetch-rr.sh
	$(DIR)dsm/vm-up.sh

dsm-storage:
	$(DIR)dsm/create-storage.sh

provision-fork:
	. $(DIR)registry/run/registry.env && \
	WEED_IMAGE=$$FORK_IMAGE WEED_PLAIN_HTTP=true WEED_DIGEST=$$FORK_DIGEST $(DIR)dsm/provision.sh

provision-upstream:
	WEED_IMAGE=chrislusf/seaweedfs:latest $(DIR)dsm/provision.sh

provision-bundled:
	$(DIR)dsm/provision.sh

verify-fork:
	$(DIR)verify/e2e.sh fork

verify-upstream:
	$(DIR)verify/e2e.sh upstream

verify-bundled:
	$(DIR)verify/e2e.sh bundled

down:
	-kill $$(cat $(DIR)dsm/out/qemu.pid 2>/dev/null) 2>/dev/null
	-$(KIND) delete cluster --name swlab
	-sudo bash -c 'kill $$(cat $(DIR)net/run/topology.pid 2>/dev/null) 2>/dev/null; sleep 3'
	-sudo docker rm -f mn.d1 2>/dev/null
	-sudo $(DIR)net/fabric-down.sh
