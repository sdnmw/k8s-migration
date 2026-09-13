FROM m.daocloud.io/docker.io/library/alpine:3.22 AS fetch
ARG KOMPOSE_VERSION=v1.38.0
ARG TARGETARCH
ARG GITHUB_DOWNLOAD_BASE=https://github.com
RUN apk add --no-cache ca-certificates curl \
    && case "${TARGETARCH}" in \
         amd64) checksum="65a6a720605bead3964e8b22d423a0763de451a236fe03de902e366cf3d9c147" ;; \
         arm64) checksum="24faf6212ec34d325e4724118098d34b9dd321cda2d9ca22efa0f04cdb909894" ;; \
         *) echo "unsupported architecture: ${TARGETARCH}" >&2; exit 1 ;; \
       esac \
    && curl --fail --location --silent --show-error --http1.1 \
       --retry 5 --retry-delay 2 --retry-all-errors --continue-at - \
       "${GITHUB_DOWNLOAD_BASE}/kubernetes/kompose/releases/download/${KOMPOSE_VERSION}/kompose-linux-${TARGETARCH}" \
       --output /kompose \
    && echo "${checksum}  /kompose" | sha256sum -c - \
    && chmod 0555 /kompose

FROM m.daocloud.io/docker.io/library/alpine:3.22
RUN addgroup -g 65532 migration && adduser -D -H -u 65532 -G migration migration
COPY --from=fetch /kompose /usr/local/bin/kompose
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/kompose"]
