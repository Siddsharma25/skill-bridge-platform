# infra/aws/keepalive-lambda/ — Render/Supabase keep-alive

CloudFormation + Lambda + CloudWatch, combined into one genuinely useful
piece rather than three token integrations bolted on separately. See
`docs/DECISIONS.md`'s "AWS keep-alive Lambda" section for the full
reasoning; this file is the practical how-to.

## What this actually does, and why

`docs/DEPLOYMENT.md` already documents two real free-tier failure modes
this project lives with:

- **Render's free web service spins down after ~15 minutes idle** — the
  next request pays a full cold start (all four backend services + the
  gateway starting up in `cmd/allinone`).
- **Supabase's free project auto-pauses after 7 days of inactivity** and
  needs a manual dashboard restore.

This stack deploys one Lambda function, triggered by an EventBridge
schedule every 10 minutes, that sends a single HTTP GET to the deployed
backend's **`/readyz`** endpoint — not `/healthz`. `/readyz` is the one
that actually pings Postgres (see `backend/cmd/allinone/main.go`'s
`readyCheckDB`), so one ping keeps **both** problems at bay: any HTTP
request resets Render's idle timer, and a real query against Supabase
resets its inactivity clock. `/healthz` alone would only solve the first
problem.

Every ping's status and latency is logged to CloudWatch Logs. A
CloudWatch Alarm watches the Lambda's own `Errors` metric and — if you
provide an email address — notifies you via SNS after **two consecutive
failed pings** (~20 minutes of sustained failure, not a single blip).

## Why this shape specifically

- **Plain CloudFormation, not SAM/CDK/Terraform.** This project already
  leans toward fewer extra CLIs where a lighter option works (see
  `docs/DECISIONS.md`'s `buf` local-plugins note) — deploying needs only
  the `aws` CLI, nothing else to install.
- **Inline `ZipFile` Lambda source, not an S3-packaged deployment.** The
  handler is ~900 bytes (well under CloudFormation's 4KB inline-code
  limit), so there's no S3 bucket to create, no `sam build`/packaging
  step — `aws cloudformation deploy` is the entire deploy process.
- **Zero npm dependencies in the handler** — it uses the Lambda Node
  runtime's built-in global `fetch`, nothing to bundle.
- **"CloudWatch Events" is now "Amazon EventBridge"** (renamed 2019) — the
  10-minute schedule technically lives under `AWS::Events::Rule`
  (EventBridge), while CloudWatch itself is used directly for the Logs
  and the Alarm. Both genuinely are "AWS CloudWatch" in the everyday
  sense; the resource type name is just EventBridge's for historical
  reasons.

## Cost: $0, permanently — not a 12-month trial

At one invocation every 10 minutes (~4,320/month):

| Service | Free tier (permanent, not trial) | This stack's usage |
|---|---|---|
| Lambda | 1M requests + 400,000 GB-seconds/month | ~4,320 requests, ~540 GB-seconds (128MB × ~15s × 4,320) |
| CloudWatch Logs | 5GB ingestion + 5GB storage/month | A few KB/month (one JSON line per invocation), 14-day retention |
| CloudWatch Alarms | 10 alarms/month | 1 |
| SNS | 1,000 email notifications/month | 0 unless something's actually failing |

Nothing here approaches the free tier's limits even after years of
running continuously.

## Deploying

Requires an AWS account (free tier, no ongoing charge for this workload)
and the [AWS CLI](https://aws.amazon.com/cli/) configured with credentials
(`aws configure`).

```bash
aws cloudformation deploy \
  --template-file infra/aws/keepalive-lambda/template.yaml \
  --stack-name skillbridge-keepalive \
  --parameter-overrides \
      TargetHealthUrl=https://<your-render-service>.onrender.com/readyz \
      AlertEmail=you@example.com \
  --capabilities CAPABILITY_IAM
```

`AlertEmail` is optional — omit it to deploy with pings-and-logs only, no
alerting. If you do set it, **AWS/SNS emails you a confirmation link right
after the stack finishes deploying** — the alarm won't actually notify you
until you click it (this is SNS's own anti-spam design; it can't be
scripted around).

Check it's working:

```bash
# Confirms the schedule is enabled and the function exists
aws lambda get-function --function-name skillbridge-keepalive-keepalive

# Tail the last few pings (one JSON line per invocation: url, status, ok, elapsedMs)
aws logs tail /aws/lambda/skillbridge-keepalive-keepalive --since 1h
```

## Tearing it down

```bash
aws cloudformation delete-stack --stack-name skillbridge-keepalive
```

Deletes every resource this stack created (the log group, IAM role,
function, schedule rule, SNS topic/subscription, and alarm) — nothing is
left behind to keep costing anything (it never cost anything at this
scale to begin with, but there's nothing orphaned either).

## Known gaps

- **Not deployed or live-verified in this repo's own environment** — no
  AWS CLI/credentials exist there. The template was validated structurally
  (YAML parses with every CloudFormation intrinsic function present, the
  inline handler's JS syntax checked directly, resource/condition/output
  references cross-checked by hand) but never run through
  `aws cloudformation validate-template` or an actual deploy. That, plus
  confirming the SNS subscription and watching one real `/readyz` ping
  succeed in CloudWatch Logs, is the one verification step left for
  whoever deploys this — see `docs/DEPLOYMENT.md`.
- **Manual deploy, like `k8s/`/`jenkins/`** — this isn't wired into any CI
  pipeline, and deliberately so: it needs real AWS credentials only the
  account owner has. Unlike the rest of this repo's "infra practice"
  folders, though, this one actually affects real production behavior
  (Render/Supabase staying warm) if you do deploy it — worth knowing that
  distinction going in.
