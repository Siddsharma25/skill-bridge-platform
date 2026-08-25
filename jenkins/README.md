# jenkins/ — local build-only practice pipeline

## What this is, and what it explicitly is not

This is hands-on Jenkins practice, run entirely on one machine, for this
project's own learning goals (see the root plan and `docs/DECISIONS.md`).
It is **not** a deploy pipeline and it **gates nothing real**:

- **GitHub Actions** (`.github/workflows/backend-ci.yml` /
  `frontend-ci.yml`) is the only pipeline an actual merge into `main`
  depends on. Nothing about this Jenkins setup is wired to GitHub at all —
  no webhook, no status check, no PR gate.
- The pipeline is **three stages only: checkout → test → docker build**.
  There is no deploy-to-`kind` stage — that flow was already exercised by
  hand in Phase 5 (`k8s/`), and re-adding it here would duplicate that
  work for no new learning value. There is no image push anywhere either
  — no registry is configured, and there is nowhere to push to.
- This Jenkins is **never exposed beyond `localhost:8090`**. Its security
  setup (a single local admin/admin account, anonymous read) is
  deliberately minimal for exactly that reason — see
  `casc/jenkins.yaml`'s own comment. Do not reuse this setup as-is for
  anything actually reachable over a network.

## How to bring it up

From the repo root:

```bash
docker compose -f jenkins/docker-compose.jenkins.yml up -d --build
```

This is fully reproducible from a clean checkout with **no manual UI
clicking**: Configuration as Code (JCasC) plus the Job DSL plugin
provision the Jenkins controller's security settings and a single
pipeline job (`skill-bridge-backend-practice`, pointing at this directory's
`Jenkinsfile`) automatically at container startup — see
`Dockerfile.jenkins` and `casc/jenkins.yaml` for exactly how, and
`casc/jobs/seed.groovy` for the job definition itself. There is no setup
wizard, no "New Item" step, no plugin-manager click-through.

Once the container is up (`docker compose -f jenkins/docker-compose.jenkins.yml logs -f jenkins`
until you see Jenkins report it's fully started), open
`http://localhost:8090` (admin / admin, unless you overrode
`JENKINS_ADMIN_PASSWORD`) and the job already exists, correctly
configured. **It does not build automatically on its own** — there is no
trigger configured (no SCM polling, no webhook, nothing scheduled), by
design, since this is meant to be run on demand, not left resident. Start
a build either via the UI ("Build Now" on the job page) or the REST API:

```bash
# 1. Get a CSRF crumb (Jenkins requires one for any state-changing call).
CRUMB=$(curl -s -u admin:admin \
  'http://localhost:8090/crumbIssuer/api/json' \
  | python3 -c 'import sys,json;d=json.load(sys.stdin);print(d["crumbRequestField"]+":"+d["crumb"])')

# 2. Trigger the build.
curl -s -u admin:admin -H "$CRUMB" -X POST \
  'http://localhost:8090/job/skill-bridge-backend-practice/build'

# 3. Poll for completion, then read the console log.
curl -s -u admin:admin 'http://localhost:8090/job/skill-bridge-backend-practice/lastBuild/api/json'
curl -s -u admin:admin 'http://localhost:8090/job/skill-bridge-backend-practice/lastBuild/consoleText'
```

## Tear down

Per this project's established "leave nothing running between phases"
pattern:

```bash
docker compose -f jenkins/docker-compose.jenkins.yml down -v
```

This removes the controller container and its named `jenkins_home`
volume — a fully clean slate for next time, matching the fact that the
whole setup reprovisions itself from nothing on the next `up --build`.

## Design choices, briefly (see `docs/DECISIONS.md` for the full writeup)

- **Container-per-stage, not a custom Jenkins agent image.** The
  controller only carries `git` and the Docker CLI (see
  `Dockerfile.jenkins`); Go, Node, `buf`, and `golangci-lint` all run
  inside short-lived containers (`golang:1.27`, `node:22-alpine`) spun up
  per-stage by the Docker Pipeline plugin (`docker.image(...).inside{}`
  in `Jenkinsfile`). Simpler to get right on a single local host than
  building and version-syncing a custom agent image against
  `backend-ci.yml`'s pinned tool versions by hand.
- **Local SCM is a bind-mounted `.git` object store, cloned via a `file://`
  URL** (`git url: 'file:///workspace/repo.git', branch: ...` in the
  Jenkinsfile) — not a GitHub remote (this repo has never been pushed
  anywhere) and not a plain working-tree bind mount either (a linked git
  worktree's `.git` is a one-line pointer file to metadata elsewhere on
  the host, which breaks a naive "just mount the working tree" approach —
  see `docs/DECISIONS.md`'s Phase 6 notes for the concrete failure this
  would hit).
- **Job provisioning via Configuration as Code + Job DSL**, reading the
  Jenkinsfile's own current text off a separate, plain bind mount at
  Jenkins startup and embedding it into the job definition — see
  `casc/jobs/seed.groovy`'s own comment for the trade-off this implies
  (editing the Jenkinsfile takes effect on the next Jenkins restart, not
  the next build — acceptable for a sandbox that's brought up and torn
  down on demand rather than left running for weeks).

## Directory contents

- `docker-compose.jenkins.yml` — the controller service, volumes, and the
  socket/repo bind mounts described above.
- `Dockerfile.jenkins` — the controller image: `jenkins/jenkins:lts-jdk17`
  plus `git`, the Docker CLI, and the plugins in `plugins.txt`.
- `plugins.txt` — unversioned plugin IDs (`jenkins-plugin-cli` resolves
  the latest version compatible with this image's Jenkins core).
- `casc/jenkins.yaml` — Configuration as Code: security realm/
  authorization strategy, plus the `jobs:` block that runs
  `casc/jobs/seed.groovy`.
- `casc/jobs/seed.groovy` — Job DSL script provisioning the single
  pipeline job.
- `Jenkinsfile` — the actual three-stage pipeline (checkout, test, build).
