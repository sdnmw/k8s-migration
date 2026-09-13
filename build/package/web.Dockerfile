ARG NODE_IMAGE=m.daocloud.io/docker.io/library/node:24.20.0-bookworm-slim
ARG NGINX_IMAGE=m.daocloud.io/docker.io/library/nginx:1.29.5-alpine
ARG NPM_REGISTRY=https://registry.npmmirror.com
FROM ${NODE_IMAGE} AS build
ARG NPM_REGISTRY
WORKDIR /src
COPY web/package.json web/package-lock.json ./
RUN npm ci --registry="${NPM_REGISTRY}"
COPY web/ ./
RUN npm run build

FROM ${NGINX_IMAGE}
COPY build/package/web.nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /src/dist /usr/share/nginx/html
EXPOSE 8080
