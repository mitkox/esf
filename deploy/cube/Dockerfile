# ESF v1.0.0-rc.2 Cube template recipe. Qualification is recorded separately.
#
# The pinned vendor base (cubesandbox-base 2026.16) ships `envd` built with Go
# 1.25.4 and the upstream Unreal runner is built with Go 1.27.0. Both carry
# Go Standard Library advisories with fixes in newer toolchains. This recipe
# rebuilds each binary from its pinned upstream source with the pinned Go
# 1.27.4 toolchain and copies the rebuilt static binaries over the vendor
# payload. The vendor base layer is otherwise unchanged.
#
# Provenance (bound by release/inventory.json and verify-release-inventory.py):
#   envd:   https://github.com/e2b-dev/infra tag 2026.16
#           commit b8ca332f435370397bf42be614b2a5b620d65d39
#           source archive sha256 0d8523b35f7bdee50668496f88411c0af75714653cd2e569c761259b95273b80
#           rebuilt binary sha256 a90f5dc798a5138c927f03c85ba98babfbe33c28617e2519e37db247afc9ae06
#   unreal: https://github.com/unreallabsai/unreal-agent tag v0.3.1
#           commit 55747f5f449c07cf76b3a0747370b5817aa52d5b
#           source archive sha256 5d734ef8129979a5238959320ba649840b49c69a1b4ebcb14b24863017f3ba93
#           rebuilt binary sha256 8ca5bac4cafb48720ed2d251ac315192c7b4becf04c550c2bda10619874971c1
# The upstream Unreal release archive and binary pins in the release inventory
# stay authoritative for the operator install path (scripts/install-agents.sh);
# the template payload is the rebuilt binary and is verified by digest here.
ARG GO_BUILDER=docker.io/library/golang@sha256:5f1b98fa02fe396ebce2a0227a0e08309e11792915b9dd2e1f71053d7e7e6666
ARG NODE_BASE=docker.io/library/node@sha256:abe7e5a82b61a8ef67b315b85497c9e65173387ccf2f4a029248184b8e020be5
ARG CUBE_BASE=ghcr.io/tencentcloud/cubesandbox-base@sha256:a1dd972a4eef85448f967daed3bda42c6be2b7f42bf64c1f4d38e63b3e935472
# Rebuild provenance (also recorded as labels on the cube-template stage).
ARG ENVD_TAG=2026.16
ARG ENVD_COMMIT=b8ca332f435370397bf42be614b2a5b620d65d39
ARG ENVD_ARCHIVE_SHA256=0d8523b35f7bdee50668496f88411c0af75714653cd2e569c761259b95273b80
ARG ENVD_BINARY_SHA256=a90f5dc798a5138c927f03c85ba98babfbe33c28617e2519e37db247afc9ae06
ARG UNREAL_TAG=v0.3.1
ARG UNREAL_COMMIT=55747f5f449c07cf76b3a0747370b5817aa52d5b
ARG UNREAL_ARCHIVE_SHA256=5d734ef8129979a5238959320ba649840b49c69a1b4ebcb14b24863017f3ba93
ARG UNREAL_BINARY_SHA256=8ca5bac4cafb48720ed2d251ac315192c7b4becf04c550c2bda10619874971c1

FROM ${GO_BUILDER} AS envd
ARG TARGETARCH
ADD --checksum=sha256:0d8523b35f7bdee50668496f88411c0af75714653cd2e569c761259b95273b80 \
    https://github.com/e2b-dev/infra/archive/b8ca332f435370397bf42be614b2a5b620d65d39.tar.gz /tmp/infra.tar.gz
RUN test "$TARGETARCH" = amd64 && \
    tar -xzf /tmp/infra.tar.gz -C /tmp && \
    cd /tmp/runtime-b8ca332f435370397bf42be614b2a5b620d65d39/packages/envd && \
    go get golang.org/x/crypto@v0.55.0 golang.org/x/mod@v0.38.0 golang.org/x/net@v0.57.0 \
           golang.org/x/sync@v0.22.0 golang.org/x/text@v0.41.0 golang.org/x/tools@v0.48.0 && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -trimpath \
      -ldflags "-X=main.commitSHA=b8ca332f -s -w" -o /out/envd . && \
    echo 'a90f5dc798a5138c927f03c85ba98babfbe33c28617e2519e37db247afc9ae06  /out/envd' | sha256sum -c - && \
    /out/envd -version && /out/envd -commit

FROM ${NODE_BASE} AS opencode
ARG TARGETARCH
WORKDIR /src/agents/opencode
COPY agents/opencode/package.json agents/opencode/package-lock.json ./
RUN test "$TARGETARCH" = amd64 && npm ci --ignore-scripts && \
    echo '523b1502d1d556998b0904235cef65eb1d8cf8362b4e2faa7ae7a170c9a44cd4  node_modules/@opencode/cli-linux-x64-baseline/bin/opencode' | sha256sum -c -

FROM ${GO_BUILDER} AS unreal
ARG TARGETARCH
ADD --checksum=sha256:5d734ef8129979a5238959320ba649840b49c69a1b4ebcb14b24863017f3ba93 \
    https://github.com/unreallabsai/unreal-agent/archive/55747f5f449c07cf76b3a0747370b5817aa52d5b.tar.gz /tmp/unreal-src.tar.gz
RUN test "$TARGETARCH" = amd64 && \
    tar -xzf /tmp/unreal-src.tar.gz -C /tmp && \
    cd /tmp/unreal-agent-55747f5f449c07cf76b3a0747370b5817aa52d5b && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
      -ldflags "-s -w" -o /out/unreal-agent-runner ./cmd/unreal-agent-runner && \
    echo '8ca5bac4cafb48720ed2d251ac315192c7b4becf04c550c2bda10619874971c1  /out/unreal-agent-runner' | sha256sum -c - && \
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
# The openssl/libssl3/libbrotli1/apt/libapt-pkg6.0 versions are the pinned
# Ubuntu security updates for the base image's advisories; refresh them with a
# new recipe when Ubuntu supersedes these versions.
#
# envd is additionally built with a pinned golang.org/x/crypto v0.55.0 (and the
# matching x/mod, x/net, x/sync, x/text, x/tools) because the upstream envd
# module graph still requires v0.49.0, which carries HIGH SSH advisories. The
# rebuilt payload is digest-pinned like the toolchain rebuild above.
RUN test "$TARGETARCH" = amd64 && apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates git python3 libatomic1 \
    openssl=3.0.2-0ubuntu1.30 libssl3=3.0.2-0ubuntu1.30 libbrotli1=1.0.9-2ubuntu0.1 \
    apt=2.4.15ubuntu0.1 libapt-pkg6.0=2.4.15ubuntu0.1 && \
    rm -rf /var/lib/apt/lists/* && \
    mkdir -p /opt/esf/agents && \
    dpkg-query -W -f='${binary:Package}=${Version}\n' ca-certificates git python3 libatomic1 \
      openssl libssl3 libbrotli1 apt libapt-pkg6.0 > /opt/esf/apt-package-versions.txt
COPY --from=envd /out/envd /usr/bin/envd
COPY --from=opencode /src/agents/opencode/node_modules/@opencode/cli-linux-x64-baseline/bin/opencode /opt/esf/agents/opencode2
COPY --from=unreal /out/unreal-agent-runner /opt/esf/agents/unreal-agent-runner
RUN chmod 0755 /usr/bin/envd /opt/esf/agents/opencode2 /opt/esf/agents/unreal-agent-runner && \
    echo 'a90f5dc798a5138c927f03c85ba98babfbe33c28617e2519e37db247afc9ae06  /usr/bin/envd' | sha256sum -c - && \
    echo '523b1502d1d556998b0904235cef65eb1d8cf8362b4e2faa7ae7a170c9a44cd4  /opt/esf/agents/opencode2' | sha256sum -c - && \
    echo '8ca5bac4cafb48720ed2d251ac315192c7b4becf04c550c2bda10619874971c1  /opt/esf/agents/unreal-agent-runner' | sha256sum -c - && \
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
