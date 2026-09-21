ARG RUNTIME_IMAGE=m.daocloud.io/gcr.io/distroless/static-debian12:nonroot
FROM prebuilt AS binaries

FROM ${RUNTIME_IMAGE}
COPY --from=binaries /server /server
COPY deploy/charts /charts
COPY build/minio/source.lock.yaml /release/minio/source.lock.yaml
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/server"]
