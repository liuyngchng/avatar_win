# Build image for cross-compiling avatar_win (Go + WebView2 + sherpa-onnx).
# Windows-only target — WebView2 and WASAPI audio are Windows-exclusive.
#
# The Go toolchain tarball is cached in build/deps/ (downloaded once by the
# host build.sh) and copied into the image, so rebuilds never hit the network.

FROM ubuntu:24.04

ARG HTTP_PROXY=""
ARG HTTPS_PROXY=""
ARG NO_PROXY=""
ENV HTTP_PROXY="$HTTP_PROXY" HTTPS_PROXY="$HTTPS_PROXY" NO_PROXY="$NO_PROXY" \
    http_proxy="$HTTP_PROXY" https_proxy="$HTTPS_PROXY" no_proxy="$NO_PROXY"

# apt proxy (only written when a proxy was actually provided).
RUN if [ -n "$HTTP_PROXY" ]; then \
        echo "Acquire::http::Proxy \"$HTTP_PROXY\";" > /etc/apt/apt.conf.d/01proxy; \
    fi; \
    if [ -n "$HTTPS_PROXY" ]; then \
        echo "Acquire::https::Proxy \"$HTTPS_PROXY\";" >> /etc/apt/apt.conf.d/01proxy; \
    fi

RUN DEBIAN_FRONTEND=noninteractive apt-get update && apt-get install -y --no-install-recommends \
    # Go + CGo essentials
    ca-certificates gcc libc6-dev pkg-config \
    # MinGW-w64 for Windows cross-compilation
    gcc-mingw-w64-x86-64 \
    # Node.js for JS obfuscation (javascript-obfuscator)
    nodejs npm \
    # Tools
    git wget zip \
    && rm -rf /var/lib/apt/lists/*

# Install javascript-obfuscator globally (used by scripts/obfuscate.js).
# npm respects HTTP_PROXY/HTTPS_PROXY from the environment.
RUN npm install -g javascript-obfuscator
# Node only resolves `require()` from local node_modules by default; NODE_PATH
# makes the global install visible to scripts/obfuscate.js.
ENV NODE_PATH=/usr/local/lib/node_modules

# Install Go 1.24 from the host-cached tarball (see build/deps/).
ARG GO_VERSION=1.24.13
COPY build/deps/go${GO_VERSION}.linux-amd64.tar.gz /tmp/go.tar.gz
RUN tar -C /usr/local -xzf /tmp/go.tar.gz && rm /tmp/go.tar.gz

ENV PATH="/usr/local/go/bin:${PATH}"
ENV GOTOOLCHAIN=local

WORKDIR /workspace