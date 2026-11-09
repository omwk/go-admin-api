# Self-hosted GitHub Actions runner for this repository.
#
# Base image registers itself on start (RUNNER_TOKEN or ACCESS_TOKEN).
# Added on top:
#   - PowerShell, because scripts/check-test-groups.ps1 uses pwsh
#   - Docker CLI, because the static job builds the runtime image
FROM myoung34/github-runner:ubuntu-jammy

USER root

RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates curl gnupg apt-transport-https \
 && install -m 0755 -d /etc/apt/keyrings \
 && curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc \
 && chmod a+r /etc/apt/keyrings/docker.asc \
 && echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo $VERSION_CODENAME) stable" \
      > /etc/apt/sources.list.d/docker.list \
 && curl -fsSL https://packages.microsoft.com/config/ubuntu/22.04/packages-microsoft-prod.deb -o /tmp/ms.deb \
 && dpkg -i /tmp/ms.deb \
 && apt-get update \
 && apt-get install -y --no-install-recommends docker-ce-cli docker-buildx-plugin powershell \
 && rm -rf /tmp/ms.deb /var/lib/apt/lists/*

# The runner user must be able to talk to the mounted Docker socket.
RUN if getent group docker >/dev/null; then usermod -aG docker runner; fi

# Toolchain baked in: every job otherwise pays for the Go download (setup-go)
# and a golangci-lint `go install` build, which made the static job the slowest
# part of CI. Pin the same versions the workflow expects.
ARG GO_VERSION=1.23.12
ARG GOLANGCI_VERSION=v1.64.8

RUN set -eux; \
    arch="$(dpkg --print-architecture)"; \
    case "$arch" in \
      amd64) goarch=amd64 ;; \
      arm64) goarch=arm64 ;; \
      *) echo "unsupported arch: $arch" >&2; exit 1 ;; \
    esac; \
    curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${goarch}.tar.gz" -o /tmp/go.tgz; \
    tar -C /usr/local -xzf /tmp/go.tgz; \
    rm /tmp/go.tgz; \
    ln -sf /usr/local/go/bin/go /usr/local/bin/go; \
    ln -sf /usr/local/go/bin/gofmt /usr/local/bin/gofmt; \
    curl -fsSL "https://github.com/golangci/golangci-lint/releases/download/${GOLANGCI_VERSION}/golangci-lint-${GOLANGCI_VERSION#v}-linux-${goarch}.tar.gz" -o /tmp/lint.tgz; \
    tar -C /tmp -xzf /tmp/lint.tgz; \
    install -m 0755 "/tmp/golangci-lint-${GOLANGCI_VERSION#v}-linux-${goarch}/golangci-lint" /usr/local/bin/golangci-lint; \
    rm -rf /tmp/lint.tgz "/tmp/golangci-lint-${GOLANGCI_VERSION#v}-linux-${goarch}"; \
    go version; \
    golangci-lint --version

ENV PATH="/usr/local/go/bin:${PATH}"
ENV GOTOOLCHAIN=local

USER runner
