ARG BASE_IMAGE
FROM prebuilt AS binaries
FROM ${BASE_IMAGE}
ARG BINARY
COPY --from=binaries /${BINARY} /${BINARY}
COPY deploy/charts /charts
ENV APP_VERSION=0.5.1
