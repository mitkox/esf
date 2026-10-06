# ESF v0.6.1 Cube template recipe. Qualification is recorded separately.
ARG NODE_BASE=docker.io/library/node@sha256:0e0ff40c39bc087845bfb27465a0df4ea419520094bc35842ff83dd8cbe6f9b6
ARG DEBIAN_BASE=docker.io/library/debian@sha256:3783cc01769c7b2b1b83a5c5ad96c815348e28ed7da68e2e3687004faa906251
ARG CUBE_BASE=ghcr.io/tencentcloud/cubesandbox-base@sha256:a1dd972a4eef85448f967daed3bda42c6be2b7f42bf64c1f4d38e63b3e935472

FROM ${NODE_BASE} AS opencode
ARG TARGETARCH
WORKDIR /src/agents/opencode
COPY agents/opencode/package.json agents/opencode/package-lock.json ./
RUN test "$TARGETARCH" = amd64 && npm ci && \
    echo 'd73436b88f2c412abb4e72056c2f6d133d9651f121952ade7fc3fe95fc9f5ddc  node_modules/@opencode/cli/bin/opencode.exe' | sha256sum -c -

FROM ${DEBIAN_BASE} AS unreal
ARG TARGETARCH
ADD --checksum=sha256:fd6e15236cc6f126ea3ddf2fc85c4be05bc5e8e4967977a89720bf95e22b304b \
    https://github.com/unreallabsai/unreal-agent/releases/download/v0.3.1/unreal-agent_0.3.1_linux_amd64.tar.gz /tmp/unreal.tar.gz
RUN test "$TARGETARCH" = amd64 && tar -xzf /tmp/unreal.tar.gz -C /tmp unreal-agent-runner && \
    echo 'd289aa2046bb79d2b063679c6e0e6b714c759f8a5ed6f969d3b3ea1eeeec03ee  /tmp/unreal-agent-runner' | sha256sum -c -

FROM ${CUBE_BASE} AS cube-template
ARG TARGETARCH
RUN test "$TARGETARCH" = amd64 && apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates git python3 && rm -rf /var/lib/apt/lists/* && \
    mkdir -p /opt/esf/agents && \
    dpkg-query -W -f='${binary:Package}=${Version}\n' ca-certificates git python3 > /opt/esf/apt-package-versions.txt
COPY --from=opencode /src/agents/opencode/node_modules/@opencode/cli/bin/opencode.exe /opt/esf/agents/opencode2
COPY --from=unreal /tmp/unreal-agent-runner /opt/esf/agents/unreal-agent-runner
RUN chmod 0755 /opt/esf/agents/opencode2 /opt/esf/agents/unreal-agent-runner && \
    echo 'd73436b88f2c412abb4e72056c2f6d133d9651f121952ade7fc3fe95fc9f5ddc  /opt/esf/agents/opencode2' | sha256sum -c - && \
    echo 'd289aa2046bb79d2b063679c6e0e6b714c759f8a5ed6f969d3b3ea1eeeec03ee  /opt/esf/agents/unreal-agent-runner' | sha256sum -c -
ENV PATH=/opt/esf/agents:${PATH}
WORKDIR /workspace
EXPOSE 49983 49999
