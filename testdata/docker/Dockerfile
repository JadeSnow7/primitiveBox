FROM debian:bookworm-slim

ARG TARGETARCH

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
      ca-certificates git bash python3 && \
    rm -rf /var/lib/apt/lists/*

COPY bin/pb-linux-${TARGETARCH} /usr/local/bin/pb
COPY bin/pb-runtimed-linux-${TARGETARCH} /usr/local/bin/pb-runtimed
COPY bin/pb-repo-adapter-linux-${TARGETARCH} /usr/local/bin/pb-repo-adapter

RUN chmod +x /usr/local/bin/pb /usr/local/bin/pb-runtimed /usr/local/bin/pb-repo-adapter && \
    mkdir -p /apps/repo /var/log/primitivebox /var/lib/primitivebox /run/primitivebox

COPY internal/runtime/manifests/repo.json /apps/repo/manifest.json

WORKDIR /workspace

CMD ["sh", "-lc", "sleep infinity"]
