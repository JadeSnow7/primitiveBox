FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates git bash \
    && rm -rf /var/lib/apt/lists/*

COPY bin/pb-linux-arm64 /usr/local/bin/pb
RUN chmod +x /usr/local/bin/pb

WORKDIR /workspace

CMD ["sh", "-lc", "sleep infinity"]
