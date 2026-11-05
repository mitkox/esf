# ESF v1.0.0-rc.1 Cube template recipe. Qualification is recorded separately.
#
# The pinned vendor base (cubesandbox-base 2026.16) ships `envd` built with Go
# 1.25.4 and the upstream Unreal runner is built with Go 1.27.0. Both carry
# Go Standard Library advisories with fixes in newer toolchains. This recipe
# rebuilds each binary from its pinned upstream source with the pinned Go
# 1.27.3 toolchain and copies the rebuilt static binaries over the vendor
# payload. The vendor base layer is otherwise unchanged.
#
# Provenance (bound by release/inventory.json and verify-release-inventory.py):
#   envd:   https://github.com/e2b-dev/infra tag 2026.16
#           commit b8ca332f435370397bf42be614b2a5b620d65d39
#           source archive sha256 0d8523b35f7bdee50668496f88411c0af75714653cd2e569c761259b95273b80
#           rebuilt binary sha256 de39ec5aa736f47288644397b673efc34519bacbe2dc0a375867b5973c434fe1
#   unreal: https://github.com/unreallabsai/unreal-agent tag v0.3.1
#           commit 55747f5f449c07cf76b3a0747370b5817aa52d5b
#           source archive sha256 5d734ef8129979a5238959320ba649840b49c69a1b4ebcb14b24863017f3ba93
#           rebuilt binary sha256 056c3492d2eeec1ad21cb75728a85acbf01657447710da4176fc524c70ce8dd9
# The upstream Unreal release archive and binary pins in the release inventory
# stay authoritative for the operator install path (scripts/install-agents.sh);
# the template payload is the rebuilt binary and is verified by digest here.
ARG GO_BUILDER=docker.io/library/golang@sha256:2920bb124f1a2e12a1d39f0cab756ae0068441ee1da7655dec7f5e374944095b
ARG NODE_BASE=docker.io/library/node@sha256:e4db4c914bf8ad241bd24804fc25d272d89bad97e68cdc8a1414862790a02c39
ARG CUBE_BASE=ghcr.io/tencentcloud/cubesandbox-base@sha256:a1dd972a4eef85448f967daed3bda42c6be2b7f42bf64c1f4d38e63b3e935472
# Rebuild provenance (also recorded as labels on the cube-template stage).
ARG ENVD_TAG=2026.16
ARG ENVD_COMMIT=b8ca332f435370397bf42be614b2a5b620d65d39
ARG ENVD_ARCHIVE_SHA256=0d8523b35f7bdee50668496f88411c0af75714653cd2e569c761259b95273b80
ARG ENVD_BINARY_SHA256=de39ec5aa736f47288644397b673efc34519bacbe2dc0a375867b5973c434fe1
ARG UNREAL_TAG=v0.3.1
ARG UNREAL_COMMIT=55747f5f449c07cf76b3a0747370b5817aa52d5b
ARG UNREAL_ARCHIVE_SHA256=5d734ef8129979a5238959320ba649840b49c69a1b4ebcb14b24863017f3ba93
ARG UNREAL_BINARY_SHA256=056c3492d2eeec1ad21cb75728a85acbf01657447710da4176fc524c70ce8dd9

FROM ${GO_BUILDER} AS envd
ARG TARGETARCH
ADD --checksum=sha256:0d8523b35f7bdee50668496f88411c0af75714653cd2e569c761259b95273b80 \
    https://github.com/e2b-dev/infra/archive/b8ca332f435370397bf42be614b2a5b620d65d39.tar.gz /tmp/infra.tar.gz
RUN test "$TARGETARCH" = amd64 && \
    tar -xzf /tmp/infra.tar.gz -C /tmp && \
    cd /tmp/runtime-b8ca332f435370397bf42be614b2a5b620d65d39/packages/envd && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -trimpath \
      -ldflags "-X=main.commitSHA=b8ca332f -s -w" -o /out/envd . && \
    echo 'de39ec5aa736f47288644397b673efc34519bacbe2dc0a375867b5973c434fe1  /out/envd' | sha256sum -c - && \
    /out/envd -version && /out/envd -commit

FROM ${NODE_BASE} AS opencode
ARG TARGETARCH
WORKDIR /src/agents/opencode
COPY agents/opencode/package.json agents/opencode/package-lock.json ./
RUN test "$TARGETARCH" = amd64 && npm ci --ignore-scripts && \
    echo '8d060e1d2fbb9bb44cf09ae6ed1f3f6a7cff070b5227e2f90970132b23a92050  node_modules/@opencode/cli-linux-x64-baseline/bin/opencode' | sha256sum -c -

FROM ${GO_BUILDER} AS unreal
ARG TARGETARCH
ADD --checksum=sha256:5d734ef8129979a5238959320ba649840b49c69a1b4ebcb14b24863017f3ba93 \
    https://github.com/unreallabsai/unreal-agent/archive/55747f5f449c07cf76b3a0747370b5817aa52d5b.tar.gz /tmp/unreal-src.tar.gz
RUN test "$TARGETARCH" = amd64 && \
    tar -xzf /tmp/unreal-src.tar.gz -C /tmp && \
    cd /tmp/unreal-agent-55747f5f449c07cf76b3a0747370b5817aa52d5b && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
      -ldflags "-s -w" -o /out/unreal-agent-runner ./cmd/unreal-agent-runner && \
    echo '056c3492d2eeec1ad21cb75728a85acbf01657447710da4176fc524c70ce8dd9  /out/unreal-agent-runner' | sha256sum -c - && \
    /out/unreal-agent-runner --help >/dev/null

FROM ${CUBE_BASE} AS cube-template
ARG TARGETARCH
# Rebuilt vendor payloads (see provenance above).
ARG CUBE_BASE \
    ENVD_TAG \
    ENVD_COMMIT \
    ENVD_ARCHIVE_SHA256 \
    ENVD_BINARY_SHA256 \
    UNREAL_TAG \
    UNREAL_COMMIT \
    UNREAL_ARCHIVE_SHA256 \
    UNREAL_BINARY_SHA256
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
COPY --from=envd /out/envd /usr/bin/envd
COPY --from=opencode /src/agents/opencode/node_modules/@opencode/cli-linux-x64-baseline/bin/opencode /opt/esf/agents/opencode2
COPY --from=unreal /out/unreal-agent-runner /opt/esf/agents/unreal-agent-runner
RUN chmod 0755 /usr/bin/envd /opt/esf/agents/opencode2 /opt/esf/agents/unreal-agent-runner && \
    echo 'de39ec5aa736f47288644397b673efc34519bacbe2dc0a375867b5973c434fe1  /usr/bin/envd' | sha256sum -c - && \
    echo '8d060e1d2fbb9bb44cf09ae6ed1f3f6a7cff070b5227e2f90970132b23a92050  /opt/esf/agents/opencode2' | sha256sum -c - && \
    echo '056c3492d2eeec1ad21cb75728a85acbf01657447710da4176fc524c70ce8dd9  /opt/esf/agents/unreal-agent-runner' | sha256sum -c - && \
    /usr/bin/envd -version && /usr/bin/envd -commit
LABEL org.opencontainers.image.title="ESF Cube template" \
      io.esf.cube.base="${CUBE_BASE}" \
      io.esf.envd.tag="${ENVD_TAG}" \
      io.esf.envd.commit="${ENVD_COMMIT}" \
      io.esf.envd.source_archive_sha256="${ENVD_ARCHIVE_SHA256}" \
      io.esf.envd.binary_sha256="${ENVD_BINARY_SHA256}" \
      io.esf.unreal.tag="${UNREAL_TAG}" \
      io.esf.unreal.commit="${UNREAL_COMMIT}" \
      io.esf.unreal.source_archive_sha256="${UNREAL_ARCHIVE_SHA256}" \
      io.esf.unreal.binary_sha256="${UNREAL_BINARY_SHA256}"
ENV PATH=/opt/esf/agents:${PATH}
WORKDIR /workspace
EXPOSE 49983 49999
