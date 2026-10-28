# ESF v0.7.1 Cube template recipe. Qualification is recorded separately.
ARG NODE_BASE=docker.io/library/node@sha256:94816674c62774da0d099a947bf4613b9e1972a2733abd0d0cd80e795f8411d0
ARG DEBIAN_BASE=docker.io/library/debian@sha256:89c1ea91e897f0fe0a3731595330bc45b8bdef4047464a07116f891a94054b31
ARG CUBE_BASE=ghcr.io/tencentcloud/cubesandbox-base@sha256:a1dd972a4eef85448f967daed3bda42c6be2b7f42bf64c1f4d38e63b3e935472

FROM ${NODE_BASE} AS opencode
ARG TARGETARCH
WORKDIR /src/agents/opencode
COPY agents/opencode/package.json agents/opencode/package-lock.json ./
RUN test "$TARGETARCH" = amd64 && npm ci --ignore-scripts && \
    echo '87a4d6ac3c1b5691d4fb5e88f473992016b37ede6611c5cb7092fcdb1d91fad0  node_modules/@opencode/cli-linux-x64-baseline/bin/opencode' | sha256sum -c -

FROM ${DEBIAN_BASE} AS unreal
ARG TARGETARCH
ADD --checksum=sha256:fd6e15236cc6f126ea3ddf2fc85c4be05bc5e8e4967977a89720bf95e22b304b \
    https://github.com/unreallabsai/unreal-agent/releases/download/v0.3.1/unreal-agent_0.3.1_linux_amd64.tar.gz /tmp/unreal.tar.gz
RUN test "$TARGETARCH" = amd64 && tar -xzf /tmp/unreal.tar.gz -C /tmp unreal-agent-runner && \
    echo 'd289aa2046bb79d2b063679c6e0e6b714c759f8a5ed6f969d3b3ea1eeeec03ee  /tmp/unreal-agent-runner' | sha256sum -c -

FROM ${CUBE_BASE} AS cube-template
ARG TARGETARCH
RUN test "$TARGETARCH" = amd64 && apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates git python3 libatomic1 && rm -rf /var/lib/apt/lists/* && \
    mkdir -p /opt/esf/agents && \
    dpkg-query -W -f='${binary:Package}=${Version}\n' ca-certificates git python3 libatomic1 > /opt/esf/apt-package-versions.txt
COPY --from=opencode /src/agents/opencode/node_modules/@opencode/cli-linux-x64-baseline/bin/opencode /opt/esf/agents/opencode2
COPY --from=unreal /tmp/unreal-agent-runner /opt/esf/agents/unreal-agent-runner
RUN chmod 0755 /opt/esf/agents/opencode2 /opt/esf/agents/unreal-agent-runner && \
    echo '87a4d6ac3c1b5691d4fb5e88f473992016b37ede6611c5cb7092fcdb1d91fad0  /opt/esf/agents/opencode2' | sha256sum -c - && \
    echo 'd289aa2046bb79d2b063679c6e0e6b714c759f8a5ed6f969d3b3ea1eeeec03ee  /opt/esf/agents/unreal-agent-runner' | sha256sum -c -
ENV PATH=/opt/esf/agents:${PATH}
WORKDIR /workspace
EXPOSE 49983 49999
