# Deploying the pgfleet dashboard on AWS (EC2 + Docker)

A single small EC2 instance running `docker compose`: the dashboard container
plus a Postgres container seeded with the 250-tenant demo fleet. No ECS, no
load balancer, no RDS: for a portfolio demo those buy nothing and cost time
and money.

The whole instance configures itself from a user-data script, so this is mostly
"launch an instance and paste a script".

## What you end up with

`http://<instance-public-ip>:8080` serving the read-only dashboard against 250
tenant schemas, three of them deliberately drifted so the UI shows red.

## 1. Launch the instance

In the EC2 console, **Launch instance**:

| Setting | Value |
| --- | --- |
| Name | `pgfleet-dashboard` |
| AMI | **Amazon Linux 2023** (the default) |
| Instance type | `t3.micro` (free-tier eligible) |
| Key pair | Create or pick one; you want SSH for troubleshooting |
| Storage | 8 GiB gp3 (the default) is enough |

The user-data script adds 2 GB of swap, because building the image (npm install
plus a Go compile) will otherwise run out of memory on a 1 GB `t3.micro`. If you
would rather not rely on swap, use `t3.small`, which is not free-tier, roughly
$15/month on-demand.

### Security group

Create a new security group with exactly two inbound rules:

| Type | Port | Source | Why |
| --- | --- | --- | --- |
| SSH | 22 | **My IP** | troubleshooting; do not open this to the world |
| Custom TCP | 8080 | `0.0.0.0/0` | the dashboard, so anyone with the link can view it |

**Do not add a rule for 5432.** Postgres only needs to be reachable from the
dashboard container on the compose network, never from the internet.

### User data

Expand **Advanced details**, scroll to **User data**, and paste the contents of
[`deploy/ec2-user-data.sh`](../deploy/ec2-user-data.sh).

Then **Launch instance**.

## 2. Wait for it to build

First boot installs Docker, clones the repo, builds the image, seeds and
migrates 250 tenants, and starts the dashboard. On a `t3.micro` expect roughly
5–10 minutes, most of it the Go and npm builds.

Watch it if you like:

```
ssh -i <your-key>.pem ec2-user@<public-ip>
sudo tail -f /var/log/cloud-init-output.log
```

The script finishes with `pgfleet dashboard is up on port 8080`.

## 3. Open it

```
http://<instance-public-ip>:8080
```

You should see 250 total tenants, 247 on the latest version, 3 behind, and 3
drifted. Clicking `tenant_087`, `tenant_142`, or `tenant_199` shows the
object- and field-level diff against `tenant_template`.

## A stable address and HTTPS

The steps above leave the dashboard on `http://<public-ip>:8080`. That address
changes if the instance is ever stopped and started, and browsers flag it as not
secure. Fixing both takes three things: a stable IP, a hostname, and a
certificate.

A publicly trusted certificate cannot be issued for a bare IP address, so a
hostname is required no matter what. Any DNS name works; the steps below use
[DuckDNS](https://www.duckdns.org), which is free and needs no domain purchase.

### 1. Elastic IP

In the EC2 console, **Elastic IPs** then **Allocate Elastic IP address**, then
**Actions, Associate** it with the instance. The public IP is now fixed for the
life of the allocation.

Cost note: since February 2024 AWS bills every public IPv4 address at roughly
$0.005/hour, so the instance is already incurring that. Associating an Elastic
IP does not add to it. An Elastic IP that is allocated but *not* attached to a
running instance is billed at the same rate, so release it when the instance is
terminated.

### 2. Hostname

Create a DuckDNS subdomain and point it at the Elastic IP. DNS propagation for
DuckDNS is effectively immediate; confirm with:

```
dig +short <subdomain>.duckdns.org
```

### 3. Security group

Open the ports the certificate authority and browsers need, and close the one
that is no longer used:

| Type | Port | Source | Why |
| --- | --- | --- | --- |
| HTTP | 80 | `0.0.0.0/0` | the ACME challenge and the redirect to HTTPS |
| HTTPS | 443 | `0.0.0.0/0` | the dashboard |
| SSH | 22 | My IP | troubleshooting |

**Remove the rule for 8080.** The app no longer publishes that port publicly; it
is bound to loopback and reached only by the proxy over the compose network.

### 4. Bring up TLS

On the instance, write the hostname into an environment file and start the proxy:

```
cd /opt/pgfleet
sudo tee .env >/dev/null <<'EOF'
PGFLEET_HOST=<subdomain>.duckdns.org
TLS_EMAIL=you@example.com
EOF
sudo docker compose --profile dashboard --profile tls up -d
```

Caddy requests a certificate on first start and renews it automatically, so
there is no cron job to maintain. Watch it happen with
`sudo docker compose logs -f caddy`; a line containing `certificate obtained
successfully` means it worked.

`https://<subdomain>.duckdns.org` now serves the dashboard, and plain HTTP
redirects to it.

If the certificate fails to issue, the cause is almost always one of: port 80
not open in the security group, DNS not yet pointing at the Elastic IP, or the
hostname in `.env` not matching the one in DNS. Let's Encrypt rate-limits
repeated failures for the same name, so fix the cause before retrying rather
than restarting in a loop.

## Troubleshooting

```
cd /opt/pgfleet
docker compose ps                                  # are both containers up?
docker compose --profile dashboard logs dashboard  # dashboard logs
docker compose logs postgres                       # database logs
sudo tail -100 /var/log/cloud-init-output.log      # the first-boot script
```

If the page loads but shows "Can't reach the pgfleet server", the UI is being
served but the API is failing. Check the dashboard logs; it is usually the DSN
or the database still starting.

To rebuild after pushing a change:

```
cd /opt/pgfleet && git pull
docker compose --profile dashboard up -d --build
```

## Cost and teardown

A `t3.micro` is free-tier eligible for the first 12 months of a new account;
after that it is a few dollars a month. Nothing else here costs money: no load
balancer, no RDS, no elastic IP (as long as it stays attached to a running
instance).

When you no longer need the live URL, **terminate** the instance (stopping it
still bills the EBS volume). Keep the Dockerfile, the compose file, and a
screen recording of the dashboard so the deployment story survives without the
bill.

## Security notes

This is a demo deployment, and it is worth being explicit about what that means:

- **The dashboard has no authentication.** Anyone with the URL can view it.
  That is fine here because everything it shows is generated demo data, but do
  not point this deployment at a real database.
- **It is strictly read-only.** The API only wraps `migrate status`,
  `drift verify`, and `drift diff`, so there are no write paths and, so a visitor
  cannot change anything.
- **The DSN is passed at runtime** via `PGFLEET_DSN` in the compose file and is
  never baked into the image. The demo credentials are throwaway; if you ever
  point this at something real, use a secret store rather than compose
  environment values.
- **Keep 5432 closed** to the internet, and SSH restricted to your own IP.
- Plain HTTP on port 8080 means traffic is unencrypted. For a demo showing
  synthetic data that is an acceptable trade; putting it behind a domain with
  TLS (Caddy or an ALB) is the obvious next step if you want it to look more
  production-like.
