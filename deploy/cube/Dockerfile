# ESF v0.7.3 Cube template recipe. Qualification is recorded separately.
ARG NODE_BASE=docker.io/library/node@sha256:e4db4c914bf8ad241bd24804fc25d272d89bad97e68cdc8a1414862790a02c39
ARG DEBIAN_BASE=docker.io/library/debian@sha256:f6b08b09a05725f9431502d3b5714f5c4576e82c1a84fa1debe0c24a7e0c3ec4
ARG CUBE_BASE=ghcr.io/tencentcloud/cubesandbox-base@sha256:a1dd972a4eef85448f967daed3bda42c6be2b7f42bf64c1f4d38e63b3e935472

FROM ${NODE_BASE} AS opencode
ARG TARGETARCH
WORKDIR /src/agents/opencode
COPY agents/opencode/package.json agents/opencode/package-lock.json ./
RUN test "$TARGETARCH" = amd64 && npm ci --ignore-scripts && \
    echo '8d060e1d2fbb9bb44cf09ae6ed1f3f6a7cff070b5227e2f90970132b23a92050  node_modules/@opencode/cli-linux-x64-baseline/bin/opencode' | sha256sum -c -

FROM ${DEBIAN_BASE} AS unreal
ARG TARGETARCH
ADD --checksum=sha256:fd6e15236cc6f126ea3ddf2fc85c4be05bc5e8e4967977a89720bf95e22b304b \
    https://github.com/unreallabsai/unreal-agent/releases/download/v0.3.1/unreal-agent_0.3.1_linux_amd64.tar.gz /tmp/unreal.tar.gz
RUN test "$TARGETARCH" = amd64 && tar -xzf /tmp/unreal.tar.gz -C /tmp unreal-agent-runner && \
    echo 'd289aa2046bb79d2b063679c6e0e6b714c759f8a5ed6f969d3b3ea1eeeec03ee  /tmp/unreal-agent-runner' | sha256sum -c -

FROM ${CUBE_BASE} AS cube-template
ARG TARGETARCH
# The openssl/libssl3/libbrotli1 versions are the pinned Ubuntu security
# updates for the base image's advisories; refresh them with a new recipe when
# Ubuntu supersedes these versions.
RUN test "$TARGETARCH" = amd64 && apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates git python3 libatomic1 \
    openssl=3.0.2-0ubuntu1.30 libssl3=3.0.2-0ubuntu1.30 libbrotli1=1.0.9-2ubuntu0.1 && \
    rm -rf /var/lib/apt/lists/* && \
    mkdir -p /opt/esf/agents && \
    dpkg-query -W -f='${binary:Package}=${Version}\n' ca-certificates git python3 libatomic1 \
      openssl libssl3 libbrotli1 > /opt/esf/apt-package-versions.txt
COPY --from=opencode /src/agents/opencode/node_modules/@opencode/cli-linux-x64-baseline/bin/opencode /opt/esf/agents/opencode2
COPY --from=unreal /tmp/unreal-agent-runner /opt/esf/agents/unreal-agent-runner
RUN chmod 0755 /opt/esf/agents/opencode2 /opt/esf/agents/unreal-agent-runner && \
    echo '8d060e1d2fbb9bb44cf09ae6ed1f3f6a7cff070b5227e2f90970132b23a92050  /opt/esf/agents/opencode2' | sha256sum -c - && \
    echo 'd289aa2046bb79d2b063679c6e0e6b714c759f8a5ed6f969d3b3ea1eeeec03ee  /opt/esf/agents/unreal-agent-runner' | sha256sum -c -
ENV PATH=/opt/esf/agents:${PATH}
WORKDIR /workspace
EXPOSE 49983 49999
