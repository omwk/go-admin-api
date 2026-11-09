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
