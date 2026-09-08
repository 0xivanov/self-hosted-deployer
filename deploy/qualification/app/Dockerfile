FROM busybox:1.36
RUN mkdir -p /www && printf 'hosting-lab-ok\n' >/www/index.html
USER 65532
CMD ["httpd", "-f", "-p", "8080", "-h", "/www"]
