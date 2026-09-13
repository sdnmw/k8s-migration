FROM node:24.20.0-bookworm AS node

FROM golang:1.26.5-bookworm
COPY --from=node /usr/local/ /usr/local/
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl git make openssh-client postgresql-client \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /workspace

