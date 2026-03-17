FROM debian:bookworm-slim

ARG TARGETARCH

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
      ca-certificates git bash python3 curl && \
    rm -rf /var/lib/apt/lists/*

COPY bin/pb-linux-${TARGETARCH} /usr/local/bin/pb
COPY bin/pb-runtimed-linux-${TARGETARCH} /usr/local/bin/pb-runtimed
COPY bin/pb-repo-adapter-linux-${TARGETARCH} /usr/local/bin/pb-repo-adapter

RUN chmod +x /usr/local/bin/pb /usr/local/bin/pb-runtimed /usr/local/bin/pb-repo-adapter && \
    mkdir -p /apps/repo /var/log/primitivebox /var/lib/primitivebox /run/primitivebox /data/.primitivebox

COPY internal/runtime/manifests/repo.json /apps/repo/manifest.json
COPY scripts/allinone-entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 8080
VOLUME ["/workspace", "/data"]

HEALTHCHECK --interval=10s --timeout=3s --retries=3 \
  CMD curl -sf http://localhost:8080/health || exit 1

ENV PB_DATA_DIR=/data

ENTRYPOINT ["/entrypoint.sh"]
