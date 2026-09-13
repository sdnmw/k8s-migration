FROM m.daocloud.io/gcr.io/distroless/static-debian12:nonroot
COPY output/worker-linux-amd64 /worker
USER nonroot:nonroot
ENTRYPOINT ["/worker"]
