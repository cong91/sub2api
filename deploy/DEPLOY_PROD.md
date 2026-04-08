# Sub2API Production Deploy (GitHub Actions build + SSH deploy + Docker + Nginx)

## What this setup does
- Build Docker image on GitHub Actions
- Push image to GHCR
- SSH into the VM
- Keep server-side `deploy/.env` as the source of truth
- Run `sub2api + postgres + redis + nginx`
- Nginx proxies `token.v-claw.org` to `sub2api:8080`

## Files
- `.github/workflows/docker-build-deploy.yml`
- `deploy/docker-compose.prod.yml`
- `deploy/nginx/nginx.conf`
- `deploy/nginx/conf.d/token.v-claw.org.conf`

## Required GitHub Secrets
Create these in repo settings -> Secrets and variables -> Actions:

- `SSH_HOST` = server IP, e.g. `35.209.156.139`
- `SSH_USER` = deploy user, e.g. `deploy`
- `SSH_PRIVATE_KEY` = private key for deploy user
- `SSH_PORT` = `22`
- `SSH_APP_DIR` = app path on server, e.g. `/home/deploy/apps/sub2api`
- `GHCR_PAT` = token that can pull private GHCR images on the server

## Server-side checklist
Run these on the VM.

### 1) Create deploy user
```bash
sudo adduser deploy
sudo usermod -aG docker deploy
```

### 2) Add SSH key for GitHub Actions deploy
As `deploy` user:
```bash
sudo -iu deploy
mkdir -p ~/.ssh
chmod 700 ~/.ssh
nano ~/.ssh/authorized_keys
chmod 600 ~/.ssh/authorized_keys
```
Paste the GitHub Actions deploy public key there.

### 3) Clone repo
```bash
mkdir -p ~/apps
cd ~/apps
git clone git@github.com:<OWNER>/sub2api.git
cd sub2api
```

### 4) Ensure env file already exists
```bash
cd ~/apps/sub2api/deploy
ls -la .env
```
Use your existing production `.env` here.

### 5) Prepare deploy directories
```bash
mkdir -p ~/apps/sub2api/deploy/data
mkdir -p ~/apps/sub2api/deploy/postgres_data
mkdir -p ~/apps/sub2api/deploy/redis_data
mkdir -p ~/apps/sub2api/deploy/nginx/conf.d
```

### 6) GHCR login test on server
```bash
echo '<GHCR_PAT>' | docker login ghcr.io -u <github-owner> --password-stdin
```

## Workflow behavior
### Build job
- runs on GitHub-hosted runner
- builds from repo root `Dockerfile`
- pushes image to `ghcr.io/<owner>/sub2api`
- tags include:
  - `latest`
  - `sha-<commit>`

### Deploy job
- SSH into VM
- reset repo to `origin/main`
- login GHCR
- run:
```bash
IMAGE_REPO=ghcr.io/<owner>/sub2api IMAGE_TAG=sha-<commit> docker compose -f docker-compose.prod.yml pull
IMAGE_REPO=ghcr.io/<owner>/sub2api IMAGE_TAG=sha-<commit> docker compose -f docker-compose.prod.yml up -d
```
- verify health:
```bash
curl http://127.0.0.1:8080/health
curl http://127.0.0.1/health
```

## Nginx / Cloudflare
Nginx listens on port 80 and proxies `token.v-claw.org` to Sub2API.
Point Cloudflare DNS for `token.v-claw.org` to the VM IP.

## First manual deploy sanity test
On server:
```bash
cd ~/apps/sub2api/deploy
docker compose -f docker-compose.prod.yml config >/dev/null
IMAGE_REPO=ghcr.io/<owner>/sub2api IMAGE_TAG=latest docker compose -f docker-compose.prod.yml pull
IMAGE_REPO=ghcr.io/<owner>/sub2api IMAGE_TAG=latest docker compose -f docker-compose.prod.yml up -d
curl http://127.0.0.1:8080/health
curl http://127.0.0.1/health
```
