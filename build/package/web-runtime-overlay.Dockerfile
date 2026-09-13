ARG BASE_IMAGE
FROM ${BASE_IMAGE}
COPY web/dist /usr/share/nginx/html
