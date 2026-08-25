// jenkins/casc/jobs/seed.groovy
//
// Provisions the single Phase 6 practice pipeline job declaratively at
// Jenkins startup, via the Job DSL plugin's Configuration as Code
// integration (see jenkins/casc/jenkins.yaml's `jobs:` section) — this is
// what replaces clicking through Jenkins's "New Item" wizard for a
// reproducible-from-a-clean-checkout setup.
//
// The Jenkinsfile's actual text is read directly off the read-only,
// plain-working-tree bind mount (see docker-compose.jenkins.yml's
// /workspace/repo mount) and embedded into the job definition as an
// inline CPS script, rather than configuring "Pipeline script from SCM"
// against the same mount. Two reasons, both documented at length in
// jenkins/README.md:
//   1. A plain bind-mounted working tree can't reliably serve as a git
//      `file://` SCM source on its own — if that working tree happens to
//      be a linked git worktree (as this repo's own agent-driven
//      development sometimes uses), its `.git` is a one-line pointer file
//      to metadata that lives elsewhere on the host, which a container
//      with only the working tree mounted can't resolve. The pipeline's
//      own Checkout stage instead clones from a SEPARATE bind mount of
//      the repo's real .git object store (see docker-compose.jenkins.yml
//      and the Jenkinsfile) — that's real git, every build, live.
//   2. This script only needs to read one small file's current text at
//      the moment Jenkins starts, not perform a git operation at all —
//      plain Groovy file IO is the simplest correct tool for that.
//
// Trade-off, stated plainly: editing jenkins/Jenkinsfile takes effect on
// the NEXT Jenkins (re)start (which re-applies CASC and re-runs this
// script), not on the next build the way "Pipeline script from SCM" would
// live-reload. Acceptable for a local practice sandbox that's routinely
// brought up and torn down per this project's established pattern, not
// something that stays running and iterated on for weeks at a time.
def jenkinsfileText = new File('/workspace/repo/jenkins/Jenkinsfile').text

pipelineJob('skill-bridge-backend-practice') {
    description('''Phase 6 practice pipeline for skill-bridge-platform: checkout -> test -> docker build.
Build-only sandbox -- gates nothing real. GitHub Actions
(.github/workflows/backend-ci.yml / frontend-ci.yml) remains the only
pipeline an actual merge depends on. See jenkins/README.md.''')
    definition {
        cps {
            script(jenkinsfileText)
            sandbox(true)
        }
    }
}
