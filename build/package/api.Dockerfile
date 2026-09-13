ARG GO_IMAGE=m.daocloud.io/docker.io/library/golang:1.26.5-bookworm
ARG RUNTIME_IMAGE=m.daocloud.io/gcr.io/distroless/static-debian12:nonroot
ARG GOPROXY=https://goproxy.cn,direct
FROM ${GO_IMAGE} AS build
ARG GOPROXY
ENV GOPROXY=${GOPROXY}
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

FROM ${RUNTIME_IMAGE}
COPY --from=build /out/server /server
COPY deploy/charts /charts
COPY build/minio/source.lock.yaml /release/minio/source.lock.yaml
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/server"]
