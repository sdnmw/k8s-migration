ARG BASE_IMAGE
FROM prebuilt AS assets
FROM ${BASE_IMAGE}
COPY build/package/web.nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=assets /dist /usr/share/nginx/html
