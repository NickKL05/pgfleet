#!/usr/bin/env bash
#
# EC2 user data for the pgfleet dashboard demo (Amazon Linux 2023).
#
# Paste this into "Advanced details -> User data" in the EC2 launch wizard. It
# runs once, as root, on first boot, and leaves a live dashboard on port 8080
# backed by the 250-tenant demo fleet with three tenants deliberately drifted.
#
# Progress and errors land in /var/log/cloud-init-output.log.
# See docs/deploy-aws.md for the full walkthrough.
set -euxo pipefail

REPO_URL="https://github.com/NickKL05/pgfleet.git"
BRANCH="feat/web-dashboard"   # switch to main once the dashboard is merged
APP_DIR="/opt/pgfleet"
COMPOSE_VERSION="v2.32.4"

# --- swap ------------------------------------------------------------------
# The image build runs npm install and a Go compile; both are memory hungry and
# will OOM on a 1 GB t3.micro. 2 GB of swap makes the free-tier size viable.
if [ ! -f /swapfile ]; then
  dd if=/dev/zero of=/swapfile bs=1M count=2048
  chmod 600 /swapfile
  mkswap /swapfile
  swapon /swapfile
  echo '/swapfile none swap sw 0 0' >>/etc/fstab
fi

# --- docker + git ----------------------------------------------------------
dnf update -y
dnf install -y docker git
systemctl enable --now docker
# Let the default login user run docker without sudo (takes effect next login).
usermod -aG docker ec2-user

# Compose v2 is not packaged on AL2023; install it as a docker CLI plugin.
# uname -m reports x86_64 or aarch64, which matches the release asset names, so
# this works on both Intel (t3) and Graviton (t4g) instances.
mkdir -p /usr/local/lib/docker/cli-plugins
curl -fsSL \
  "https://github.com/docker/compose/releases/download/${COMPOSE_VERSION}/docker-compose-linux-$(uname -m)" \
  -o /usr/local/lib/docker/cli-plugins/docker-compose
chmod +x /usr/local/lib/docker/cli-plugins/docker-compose

# --- application -----------------------------------------------------------
git clone --branch "$BRANCH" "$REPO_URL" "$APP_DIR"
cd "$APP_DIR"

# Postgres first: its init scripts seed 250 tenant schemas plus the canonical
# tenant_template on first start.
docker compose up -d postgres
until docker compose exec -T postgres pg_isready -U pgfleet -d fleet >/dev/null 2>&1; do
  sleep 2
done

# Build the dashboard image (Vue build -> static Go build -> distroless).
docker compose --profile dashboard build

# Bring the fleet to the latest migration, then break three tenants on purpose
# so the dashboard shows real drift instead of an all-green wall.
docker compose run --rm dashboard migrate up
docker compose exec -T postgres psql -U pgfleet -d fleet -q -f - <demo/introduce_drift.sql

# --- serve -----------------------------------------------------------------
docker compose --profile dashboard up -d

echo "pgfleet dashboard is up on port 8080"
