FROM m.daocloud.io/docker.io/library/alpine:3.22 AS fetch
ARG KOPIA_VERSION=0.23.1
ARG TARGETARCH
ARG GITHUB_DOWNLOAD_BASE=https://github.com
RUN apk add --no-cache ca-certificates curl \
    && case "${TARGETARCH}" in \
         amd64) artifact="kopia-${KOPIA_VERSION}-linux-x64.tar.gz"; checksum="416d0f84a3dbb321a8b2d8f0997b1a0a6e915babe79ee76fa6e4d2bd1e1c5178" ;; \
         arm64) artifact="kopia-${KOPIA_VERSION}-linux-arm64.tar.gz"; checksum="a4ffbc019e0b0f932e2632054e73ec521dc1e80172a00095369c53ecf4e5a6cb" ;; \
         *) echo "unsupported architecture: ${TARGETARCH}" >&2; exit 1 ;; \
       esac \
    && curl --fail --location --silent --show-error --http1.1 \
       --retry 5 --retry-delay 2 --retry-all-errors --continue-at - \
       "${GITHUB_DOWNLOAD_BASE}/kopia/kopia/releases/download/v${KOPIA_VERSION}/${artifact}" \
       --output /kopia.tar.gz \
    && echo "${checksum}  /kopia.tar.gz" | sha256sum -c - \
    && tar -xzf /kopia.tar.gz -C /tmp \
    && find /tmp -type f -name kopia -exec cp {} /kopia \; \
    && chmod 0555 /kopia

FROM m.daocloud.io/docker.io/library/alpine:3.22
RUN apk add --no-cache ca-certificates \
    && addgroup -g 65532 migration \
    && adduser -D -H -u 65532 -G migration migration \
    && mkdir -p /app/config /app/cache /app/logs \
    && chown -R 65532:65532 /app
COPY --from=fetch /kopia /usr/local/bin/kopia
ENV KOPIA_CONFIG_PATH=/app/config/repository.config \
    KOPIA_CACHE_DIRECTORY=/app/cache \
    KOPIA_LOG_DIR=/app/logs \
    KOPIA_PERSIST_CREDENTIALS_ON_CONNECT=false \
    KOPIA_CHECK_FOR_UPDATES=false
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/kopia"]
