ARG RUNTIME_IMAGE=m.daocloud.io/gcr.io/distroless/static-debian12:nonroot
FROM prebuilt AS binaries

FROM ${RUNTIME_IMAGE}
COPY --from=binaries /worker /worker
USER nonroot:nonroot
ENTRYPOINT ["/worker"]
