ARG NGINX_IMAGE=m.daocloud.io/docker.io/library/nginx:1.29.5-alpine
FROM prebuilt AS assets

FROM ${NGINX_IMAGE}
COPY build/package/web.nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=assets /dist /usr/share/nginx/html
EXPOSE 8080
