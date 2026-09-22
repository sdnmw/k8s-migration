SHELL := /bin/sh

GO_IMAGE ?= golang:1.26.5-bookworm
NODE_IMAGE ?= node:24.20.0-bookworm-slim
ROOT_DIR := $(CURDIR)
LOCAL_GO := $(firstword $(wildcard $(ROOT_DIR)/.cache/toolchains/go/bin/go) $(shell command -v go 2>/dev/null))
LOCAL_NPM := $(shell command -v npm 2>/dev/null)

ifeq ($(LOCAL_GO),)
GO_RUN := docker run --rm -v "$(ROOT_DIR):/workspace" -w /workspace $(GO_IMAGE) go
GOFMT_RUN := docker run --rm -v "$(ROOT_DIR):/workspace" -w /workspace $(GO_IMAGE) gofmt
else
GO_RUN := $(LOCAL_GO)
GOFMT_RUN := $(dir $(LOCAL_GO))gofmt
endif

ifeq ($(LOCAL_NPM),)
NPM_RUN := docker run --rm -v "$(ROOT_DIR)/web:/workspace" -w /workspace $(NODE_IMAGE) npm
else
NPM_RUN := cd web && npm
endif

.PHONY: help bootstrap build test lint api-lint go-build go-test go-vet go-fmt-check web-install web-build web-test web-lint vendor-velero-chart offline-pack offline-verify offline-lab-amd64 sks-minio-acceptance sks-nfs-acceptance sks-velero-acceptance sks-platform-acceptance kind-e2e sks-control-e2e sks-existing-velero-e2e sks-fsb-e2e sks-compose-e2e dev dev-secrets down clean

help:
	@echo "SKS Migration Center development targets"
	@echo "  make bootstrap     Install frontend dependencies"
	@echo "  make build         Build backend binaries and frontend"
	@echo "  make test          Run backend and frontend tests"
	@echo "  make lint          Run formatting and lint checks"
	@echo "  make offline-pack  Build a verified offline tar.gz from OFFLINE_SOURCE"
	@echo "  make offline-verify Verify an extracted bundle at OFFLINE_SOURCE"
	@echo "  make offline-lab-amd64 Build the complete one-time linux/amd64 offline bundle"
	@echo "  make vendor-velero-chart Fetch the checksum-locked official Velero chart"
	@echo "  make sks-minio-acceptance Run the opt-in MinIO persistence test on a real SKS cluster"
	@echo "  make sks-nfs-acceptance Run the opt-in NFS provisioning and remount test on a real SKS cluster"
	@echo "  make sks-velero-acceptance Install Velero on source/target SKS and run a real Backup"
	@echo "  make sks-compose-e2e Run Compose/Kopia migration from an SSH host to a real SKS cluster"
	@echo "  make sks-platform-acceptance Install the full platform on SKS and verify browser login"
	@echo "  make dev           Start the local stack"
	@echo "  make down          Stop the local stack"

bootstrap: web-install

build: go-build web-build

test: go-test web-test

lint: go-fmt-check go-vet web-lint api-lint

api-lint:
	$(NPM_RUN) run api:lint

go-build:
	mkdir -p dist
	$(GO_RUN) build -trimpath -o dist/server ./cmd/server
	$(GO_RUN) build -trimpath -o dist/worker ./cmd/worker
	$(GO_RUN) build -trimpath -o dist/offline ./cmd/offline

go-test:
	$(GO_RUN) test ./cmd/... ./internal/...

go-vet:
	$(GO_RUN) vet ./cmd/... ./internal/...

go-fmt-check:
	test -z "$$($(GOFMT_RUN) -l $$(find cmd internal -name '*.go' -type f))"

web-install:
	$(NPM_RUN) ci

web-build:
	$(NPM_RUN) run build

web-test:
	$(NPM_RUN) run test

web-lint:
	$(NPM_RUN) run lint

offline-pack: go-build
	test -n "$(OFFLINE_SOURCE)" && test -n "$(OFFLINE_OUTPUT)"
	./dist/offline pack --source "$(OFFLINE_SOURCE)" --output "$(OFFLINE_OUTPUT)"

offline-verify: go-build
	test -n "$(OFFLINE_SOURCE)"
	./dist/offline verify --directory "$(OFFLINE_SOURCE)"

offline-lab-amd64:
	./scripts/build-lab-offline-bundle.sh "$(if $(OFFLINE_OUTPUT),$(OFFLINE_OUTPUT),$(ROOT_DIR)/output/sks-migration-center-0.4.0-amd64)"

vendor-velero-chart:
	./scripts/vendor-velero-chart.sh deploy/charts/velero-12.1.0.tgz

sks-minio-acceptance:
	test -n "$(TEST_SKS_KUBECONFIG)"
	TEST_SKS_KUBECONFIG="$(TEST_SKS_KUBECONFIG)" $(GO_RUN) test -v ./internal/acceptance -run '^TestMinIOOnSKS$$' -count=1 -timeout=35m

sks-nfs-acceptance:
	test -n "$(TEST_SKS_KUBECONFIG)"
	TEST_SKS_KUBECONFIG="$(TEST_SKS_KUBECONFIG)" TEST_SKS_NFS_SOURCE_SC="$(TEST_SKS_NFS_SOURCE_SC)" TEST_SKS_NFS_TARGET_SC="$(TEST_SKS_NFS_TARGET_SC)" $(GO_RUN) test -v ./internal/acceptance -run '^TestNFSOnSKS$$' -count=1 -timeout=25m

sks-velero-acceptance:
	test -n "$(TEST_SKS_SOURCE_KUBECONFIG)" && test -n "$(TEST_SKS_TARGET_KUBECONFIG)"
	TEST_SKS_SOURCE_KUBECONFIG="$(TEST_SKS_SOURCE_KUBECONFIG)" TEST_SKS_TARGET_KUBECONFIG="$(TEST_SKS_TARGET_KUBECONFIG)" TEST_VELERO_IMAGE="$(TEST_VELERO_IMAGE)" TEST_VELERO_AWS_PLUGIN_IMAGE="$(TEST_VELERO_AWS_PLUGIN_IMAGE)" $(GO_RUN) test -v ./internal/acceptance -run '^TestVeleroOnSKS$$' -count=1 -timeout=45m

kind-e2e:
	./scripts/kind-dual-e2e.sh

sks-control-e2e:
	test -n "$(TEST_SKS_SOURCE_KUBECONFIG)" && test -n "$(TEST_SKS_TARGET_KUBECONFIG)" && test -n "$(TEST_SKS_WORKLOAD_IMAGE)"
	TEST_KIND_SOURCE_KUBECONFIG="$(TEST_SKS_SOURCE_KUBECONFIG)" TEST_KIND_TARGET_KUBECONFIG="$(TEST_SKS_TARGET_KUBECONFIG)" TEST_KIND_WORKLOAD_IMAGE="$(TEST_SKS_WORKLOAD_IMAGE)" TEST_KIND_WORKLOAD_PORT="$(TEST_SKS_WORKLOAD_PORT)" $(GO_RUN) test -v ./internal/acceptance -run '^TestKindDualClusterControlPath$$' -count=1 -timeout=5m

sks-existing-velero-e2e:
	test -n "$(TEST_SKS_SOURCE_KUBECONFIG)" && test -n "$(TEST_SKS_TARGET_KUBECONFIG)" && test -n "$(TEST_SKS_EXISTING_VELERO_BSL)"
	TEST_SKS_SOURCE_KUBECONFIG="$(TEST_SKS_SOURCE_KUBECONFIG)" TEST_SKS_TARGET_KUBECONFIG="$(TEST_SKS_TARGET_KUBECONFIG)" TEST_SKS_EXISTING_VELERO_BSL="$(TEST_SKS_EXISTING_VELERO_BSL)" $(GO_RUN) test -v ./internal/acceptance -run '^TestExistingVeleroCrossClusterOnSKS$$' -count=1 -timeout=12m

sks-fsb-e2e:
	test -n "$(TEST_SKS_SOURCE_KUBECONFIG)" && test -n "$(TEST_SKS_TARGET_KUBECONFIG)" && test -n "$(TEST_SKS_EXISTING_VELERO_BSL)" && test -n "$(TEST_SKS_FSB_SOURCE_SC)" && test -n "$(TEST_SKS_FSB_TARGET_SC)" && test -n "$(TEST_SKS_FSB_IMAGE)"
	TEST_SKS_SOURCE_KUBECONFIG="$(TEST_SKS_SOURCE_KUBECONFIG)" TEST_SKS_TARGET_KUBECONFIG="$(TEST_SKS_TARGET_KUBECONFIG)" TEST_SKS_EXISTING_VELERO_BSL="$(TEST_SKS_EXISTING_VELERO_BSL)" TEST_SKS_FSB_SOURCE_SC="$(TEST_SKS_FSB_SOURCE_SC)" TEST_SKS_FSB_TARGET_SC="$(TEST_SKS_FSB_TARGET_SC)" TEST_SKS_FSB_ACCESS_MODE="$(TEST_SKS_FSB_ACCESS_MODE)" TEST_SKS_FSB_IMAGE="$(TEST_SKS_FSB_IMAGE)" $(GO_RUN) test -v ./internal/acceptance -run '^TestExistingVeleroFSBCrossClusterOnSKS$$' -count=1 -timeout=22m

sks-compose-e2e:
	test -n "$(TEST_SKS_TARGET_KUBECONFIG)" && test -n "$(TEST_COMPOSE_SSH_ENDPOINT)" && test -n "$(TEST_COMPOSE_SSH_PASSWORD)" && test -n "$(TEST_COMPOSE_SSH_HOST_KEY_FINGERPRINT)"
	TEST_SKS_TARGET_KUBECONFIG="$(TEST_SKS_TARGET_KUBECONFIG)" TEST_COMPOSE_SSH_ENDPOINT="$(TEST_COMPOSE_SSH_ENDPOINT)" TEST_COMPOSE_SSH_USERNAME="$(TEST_COMPOSE_SSH_USERNAME)" TEST_COMPOSE_SSH_PASSWORD="$(TEST_COMPOSE_SSH_PASSWORD)" TEST_COMPOSE_SSH_HOST_KEY_FINGERPRINT="$(TEST_COMPOSE_SSH_HOST_KEY_FINGERPRINT)" TEST_COMPOSE_KOMPOSE_IMAGE="$(TEST_COMPOSE_KOMPOSE_IMAGE)" TEST_COMPOSE_KOPIA_IMAGE="$(TEST_COMPOSE_KOPIA_IMAGE)" TEST_COMPOSE_SOURCE_KOPIA_IMAGE="$(TEST_COMPOSE_SOURCE_KOPIA_IMAGE)" $(GO_RUN) test -v ./internal/acceptance -run '^TestComposeKopiaCrossClusterOnSKS$$' -count=1 -timeout=22m

sks-platform-acceptance:
	test -n "$(TEST_SKS_KUBECONFIG)" && test -n "$(TEST_PLATFORM_ADMIN_PASSWORD_FILE)" && test -n "$(TEST_PLATFORM_MASTER_KEY_FILE)"
	TEST_SKS_KUBECONFIG="$(TEST_SKS_KUBECONFIG)" TEST_PLATFORM_ADMIN_PASSWORD_FILE="$(TEST_PLATFORM_ADMIN_PASSWORD_FILE)" TEST_PLATFORM_MASTER_KEY_FILE="$(TEST_PLATFORM_MASTER_KEY_FILE)" $(GO_RUN) test -v ./internal/acceptance -run '^TestPlatformOnSKS$$' -count=1 -timeout=35m

dev-secrets:
	./scripts/dev-secrets.sh .data/secrets

dev: dev-secrets
	docker compose up --build

down:
	docker compose down

clean:
	rm -rf bin dist coverage web/dist web/coverage
