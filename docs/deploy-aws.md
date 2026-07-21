# Deploying the pgfleet dashboard on AWS (EC2 + Docker)

A single small EC2 instance running `docker compose`: the dashboard container
plus a Postgres container seeded with the 250-tenant demo fleet. No ECS, no
load balancer, no RDS — for a portfolio demo those buy nothing and cost time
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
| Key pair | Create or pick one — you want SSH for troubleshooting |
| Storage | 8 GiB gp3 (the default) is enough |

The user-data script adds 2 GB of swap, because building the image (npm install
plus a Go compile) will otherwise run out of memory on a 1 GB `t3.micro`. If you
would rather not rely on swap, use `t3.small` — it is not free-tier, roughly
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

## Troubleshooting

```
cd /opt/pgfleet
docker compose ps                                  # are both containers up?
docker compose --profile dashboard logs dashboard  # dashboard logs
docker compose logs postgres                       # database logs
sudo tail -100 /var/log/cloud-init-output.log      # the first-boot script
```

If the page loads but shows "Can't reach the pgfleet server", the UI is being
served but the API is failing — check the dashboard logs; it is usually the DSN
or the database still starting.

To rebuild after pushing a change:

```
cd /opt/pgfleet && git pull
docker compose --profile dashboard up -d --build
```

## Cost and teardown

A `t3.micro` is free-tier eligible for the first 12 months of a new account;
after that it is a few dollars a month. Nothing else here costs money — no load
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
  `drift verify`, and `drift diff` — there are no write paths, so a visitor
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
