# Official release assets; hashes from the matching .sha256sum files.
FROM alpine:3.21 AS minio-amd64
ADD --checksum=sha256:88182336ab5793488f2caad54846056b330f0dcb5c488b60d52434beeebae3ca https://github.com/minio/minio/releases/download/RELEASE.2024-12-18T13-15-44Z/minio.linux-amd64.RELEASE.2024-12-18T13-15-44Z /usr/local/bin/minio

FROM alpine:3.21 AS minio-arm64
ADD --checksum=sha256:455c9b3093b7c1e6153c85cfd745c7c401a92497346ba24dbe45775c7b3ab1cd https://github.com/minio/minio/releases/download/RELEASE.2024-12-18T13-15-44Z/minio.linux-arm64.RELEASE.2024-12-18T13-15-44Z /usr/local/bin/minio

FROM minio-${TARGETARCH} AS runtime
RUN chmod 755 /usr/local/bin/minio \
    && addgroup -S minio && adduser -S -G minio minio \
    && mkdir /data && chown minio:minio /data
USER minio:minio
EXPOSE 9000 9001
ENTRYPOINT ["/usr/local/bin/minio"]
CMD ["server", "/data", "--console-address", ":9001"]
