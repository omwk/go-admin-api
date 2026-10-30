# Official release assets; hashes from the matching .sha256sum files.
FROM alpine:3.21
ARG TARGETARCH
RUN case "$TARGETARCH" in \
      amd64) sha=88182336ab5793488f2caad54846056b330f0dcb5c488b60d52434beeebae3ca ;; \
      arm64) sha=455c9b3093b7c1e6153c85cfd745c7c401a92497346ba24dbe45775c7b3ab1cd ;; \
      *) echo "Unsupported MinIO architecture" >&2; exit 1 ;; \
    esac \
    && wget -T 60 -q -O /usr/local/bin/minio "https://github.com/minio/minio/releases/download/RELEASE.2024-12-18T13-15-44Z/minio.linux-${TARGETARCH}.RELEASE.2024-12-18T13-15-44Z" \
    && echo "$sha  /usr/local/bin/minio" | sha256sum -c - \
    && chmod 755 /usr/local/bin/minio \
    && addgroup -S minio && adduser -S -G minio minio \
    && mkdir /data && chown minio:minio /data
USER minio:minio
EXPOSE 9000 9001
ENTRYPOINT ["/usr/local/bin/minio"]
CMD ["server", "/data", "--console-address", ":9001"]
