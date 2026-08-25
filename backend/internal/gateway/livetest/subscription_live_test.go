//go:build live

// Package livetest holds Phase 3.5's live, end-to-end proof that the
// onNotification GraphQL subscription actually works over a real
// WebSocket connection against a real running stack — not a unit test
// with a fake gRPC client or a fake Redis, but an actual `graphql-ws`
// (graphql-transport-ws subprotocol) client hand-speaking the protocol
// against a live api-gateway, with real auth-service/skills-service/
// users-service/jobs-service processes and real Postgres/Redis/Kafka/
// RabbitMQ behind it.
//
// Gated behind the `live` build tag specifically so `go build ./...`,
// `go vet ./...`, and a bare `go test ./...` never pick this package up —
// this codebase's CI stays DB/broker-free by design (see
// docs/DECISIONS.md), and these tests need a real stack up first. Run
// with:
//
//	go test -tags live ./internal/gateway/livetest/... -run . -v
//
// against the stack brought up per docs/DECISIONS.md's Phase 3.5
// verification notes: docker/docker-compose.infra.yml (Redis, Kafka,
// RabbitMQ), a throwaway local Postgres migrated per
// backend/migrations/000_bootstrap.sql + `make migrate-up`, and all five
// Go services (auth, skills, users, jobs, api-gateway) running via `go
// run ./cmd/<service>`. Every URL below is overridable via environment
// variable for a non-default local setup; defaults match this repo's
// documented dev ports (see backend/.env.example).
package livetest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

var (
	httpURL  = envOr("LIVETEST_GRAPHQL_HTTP_URL", "http://localhost:8080/query")
	wsURL    = envOr("LIVETEST_GRAPHQL_WS_URL", "ws://localhost:8080/query")
	redisURL = envOr("LIVETEST_REDIS_URL", "redis://localhost:6379/0")
)

// --- minimal GraphQL HTTP client -------------------------------------------------

type gqlRequest struct {
	Query string `json:"query"`
}

type gqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// doGraphQL POSTs query to httpURL (optionally bearer-authenticated) and
// returns the decoded "data" object as a generic map. Fails the test
// immediately on a transport error, a non-200 status, or a GraphQL-layer
// error — every call site below expects success, since setup steps
// (register/createSkill/addUserSkill/createJob) are preconditions for the
// subscription assertions, not what's under test themselves.
func doGraphQL(t *testing.T, query, token string) map[string]any {
	t.Helper()
	body, err := json.Marshal(gqlRequest{Query: query})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, httpURL, strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v (is the live stack actually running? see this file's package doc comment)", httpURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	var gr gqlResponse
	if err := json.NewDecoder(resp.Body).Decode(&gr); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(gr.Errors) > 0 {
		t.Fatalf("GraphQL error: %s", gr.Errors[0].Message)
	}

	var data map[string]any
	if err := json.Unmarshal(gr.Data, &data); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	return data
}

// --- minimal graphql-transport-ws client, hand-rolled per the protocol ----------
//
// https://github.com/enisdenjo/graphql-ws/blob/master/PROTOCOL.md
//
// Deliberately not using any GraphQL client library — the point of this
// test is to prove the wire protocol api-gateway actually speaks, the same
// way a real browser client (Apollo, urql, the graphql-ws npm package)
// would drive it, with nothing shared with the server's own
// implementation.

type wsMsg struct {
	ID      string          `json:"id,omitempty"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

func wsSend(t *testing.T, conn *websocket.Conn, ctx context.Context, msg wsMsg) {
	t.Helper()
	b, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal ws message: %v", err)
	}
	if err := conn.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatalf("write ws message: %v", err)
	}
}

func wsRead(t *testing.T, conn *websocket.Conn, ctx context.Context) (wsMsg, error) {
	t.Helper()
	_, data, err := conn.Read(ctx)
	if err != nil {
		return wsMsg{}, err
	}
	var msg wsMsg
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("decode ws message %q: %v", string(data), err)
	}
	return msg, nil
}

// connectAndInit dials wsURL negotiating the graphql-transport-ws
// subprotocol, sends connection_init with token (however VerifyToken
// wants it — see cmd/api-gateway/main.go's wsInitFunc), and returns the
// open connection after observing connection_ack. Fails the test if
// anything but a clean ack comes back — callers that specifically want to
// test the *rejection* path use dialAndInitExpectRejection instead.
func connectAndInit(t *testing.T, token string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		Subprotocols: []string{"graphql-transport-ws"},
	})
	if err != nil {
		t.Fatalf("dial %s: %v", wsURL, err)
	}

	payload, err := json.Marshal(map[string]string{"Authorization": "Bearer " + token})
	if err != nil {
		t.Fatalf("marshal init payload: %v", err)
	}
	wsSend(t, conn, ctx, wsMsg{Type: "connection_init", Payload: payload})

	msg, err := wsRead(t, conn, ctx)
	if err != nil {
		t.Fatalf("expected connection_ack, got a read error: %v", err)
	}
	if msg.Type != "connection_ack" {
		t.Fatalf("expected connection_ack, got %+v", msg)
	}
	return conn
}

// --- the actual Phase 3.5 proof --------------------------------------------------

// notificationPayload mirrors the GraphQL Notification type's shape (see
// schema.graphqls) — what a real subscribed client actually receives.
type notificationPayload struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Message   string `json:"message"`
	CreatedAt string `json:"createdAt"`
}

// TestOnNotification_JobMatchedLivePush is the single most important proof
// for this phase: register a real user, give them a real gRPC- and
// SQL-backed matching skill, open a genuinely authenticated WebSocket
// subscription BEFORE the matching job exists (this ordering matters —
// see docs/DECISIONS.md's Phase 3.5 notes on why job.matched, not
// auth-service's registration-time publish, is what's provable live),
// then create a job requiring that skill through the ordinary
// createJob mutation and assert the subscribed client receives a `next`
// message carrying the job_match notification within a reasonable
// timeout — driven entirely by jobs-service's Phase 2 matching worker
// publishing to RabbitMQ's notifications.realtime, api-gateway's
// consumer republishing it on Redis pub/sub, and this client's own
// onNotification subscription forwarding it, with no polling anywhere in
// the chain.
func TestOnNotification_JobMatchedLivePush(t *testing.T) {
	email := fmt.Sprintf("livetest-%s@example.com", uuid.NewString())

	registerData := doGraphQL(t, fmt.Sprintf(
		`mutation { register(email: %q, password: "password123456") { accessToken userId } }`, email), "")
	register := registerData["register"].(map[string]any)
	token := register["accessToken"].(string)
	userID := register["userId"].(string)
	t.Logf("registered user_id=%s email=%s", userID, email)

	skillName := "LiveTest Skill " + uuid.NewString()
	skillData := doGraphQL(t, fmt.Sprintf(
		`mutation { createSkill(name: %q, category: "LiveTest") { id } }`, skillName), "")
	skillID := skillData["createSkill"].(map[string]any)["id"].(string)
	t.Logf("created skill_id=%s", skillID)

	doGraphQL(t, fmt.Sprintf(
		`mutation { addUserSkill(skillId: %q, proficiency: "expert") }`, skillID), token)

	// Give jobs-service's Kafka snapshot-projection consumer a moment to
	// process user.skills.updated into jobs.user_skill_snapshot — the
	// matching worker (triggered below by createJob's job.posted publish)
	// reads only that local projection, never users-service directly (see
	// docs/DECISIONS.md's "why jobs-service doesn't just query
	// users-service's database"). This is the one polling-flavored wait in
	// this test, and it's for Kafka event-carried-state-transfer
	// convergence, not for the WebSocket push itself.
	time.Sleep(3 * time.Second)

	// Open the subscription now — genuinely listening before the matching
	// job is created, which is the entire point of using job.matched
	// (not the registration-time welcome notification) as this phase's
	// live-verification trigger.
	conn := connectAndInit(t, token)
	defer func() { _ = conn.CloseNow() }()

	subCtx, subCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer subCancel()
	wsSend(t, conn, subCtx, wsMsg{
		ID:      "sub-1",
		Type:    "subscribe",
		Payload: json.RawMessage(`{"query":"subscription { onNotification { id type message createdAt } }"}`),
	})

	// Now trigger job.posted -> the matching worker -> job_matches upsert
	// -> job.matched (Kafka) + a RabbitMQ notifications.realtime publish
	// (Phase 3.5) -> api-gateway's bridge -> Redis PUBLISH -> this
	// connection's live SUBSCRIBE.
	jobData := doGraphQL(t, fmt.Sprintf(
		`mutation { createJob(title: "LiveTest Job", description: "requires %s", requiredSkillIds: [%q]) { id } }`,
		skillName, skillID), "")
	jobID := jobData["createJob"].(map[string]any)["id"].(string)
	t.Logf("created job_id=%s, waiting for the live push...", jobID)

	readCtx, readCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer readCancel()

	for {
		msg, err := wsRead(t, conn, readCtx)
		if err != nil {
			t.Fatalf("timed out waiting for a `next` message carrying the job_match notification: %v", err)
		}
		if msg.Type == "ping" {
			wsSend(t, conn, readCtx, wsMsg{Type: "pong"})
			continue
		}
		if msg.Type != "next" {
			t.Fatalf("expected a `next` message, got %+v", msg)
		}

		var next struct {
			Data struct {
				OnNotification notificationPayload `json:"onNotification"`
			} `json:"data"`
		}
		if err := json.Unmarshal(msg.Payload, &next); err != nil {
			t.Fatalf("decode next payload %q: %v", string(msg.Payload), err)
		}
		notif := next.Data.OnNotification

		if notif.Type != "job_match" {
			// A stray welcome notification landing here would be a real
			// bug (this user's registration happened before the
			// subscription opened, so per docs/DECISIONS.md it should
			// have been silently lost, not delivered late) — fail loudly
			// rather than silently continuing past it.
			t.Fatalf("expected notification type job_match, got %q (full: %+v)", notif.Type, notif)
		}

		t.Logf("LIVE PUSH RECEIVED: %+v", notif)
		if notif.ID == "" {
			t.Error("expected a non-empty notification id")
		}
		if notif.Message == "" {
			t.Error("expected a non-empty notification message")
		}
		if notif.CreatedAt == "" {
			t.Error("expected a non-empty createdAt")
		}
		break
	}

	wsSend(t, conn, readCtx, wsMsg{ID: "sub-1", Type: "complete"})
}

// TestOnNotification_InvalidTokenRejected is the negative case: a
// connection_init with no usable token must be rejected (the connection
// closes, or a message is sent that is NOT connection_ack), never
// silently treated as an anonymous-but-accepted connection.
func TestOnNotification_InvalidTokenRejected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		Subprotocols: []string{"graphql-transport-ws"},
	})
	if err != nil {
		t.Fatalf("dial %s: %v", wsURL, err)
	}
	defer func() { _ = conn.CloseNow() }()

	payload, _ := json.Marshal(map[string]string{"Authorization": "Bearer not-a-real-jwt"})
	wsSend(t, conn, ctx, wsMsg{Type: "connection_init", Payload: payload})

	msg, err := wsRead(t, conn, ctx)
	if err == nil && msg.Type == "connection_ack" {
		t.Fatalf("expected the connection to be rejected for an invalid token, but got connection_ack")
	}
	// Either a read error (the socket was closed, the expected outcome
	// per wsInitFunc returning a non-nil error — see
	// cmd/api-gateway/main.go) or some non-ack message is an acceptable
	// rejection signal; connection_ack specifically is the only
	// unacceptable outcome, asserted above.
	t.Logf("connection correctly rejected: err=%v msg=%+v", err, msg)
}

// TestOnNotification_DisconnectReleasesRedisSubscription is Phase 3.5's
// leak test: open a real subscription (which opens a real Redis
// SUBSCRIBE on realtime:user:<id> — see internal/gateway/graph/
// schema.resolvers.go's OnNotification and internal/gateway/realtime),
// confirm via Redis's own PUBSUB NUMSUB that exactly one subscriber
// exists on that channel, then disconnect the WebSocket client and
// confirm PUBSUB NUMSUB drops back to zero — proving the per-connection
// goroutine's ctx.Done() cleanup path (Subscription.Close(), closing the
// output channel) actually runs on disconnect rather than leaking a
// live Redis subscription per dropped client, which is exactly the "real
// bug" this phase's task explicitly called out to test for.
func TestOnNotification_DisconnectReleasesRedisSubscription(t *testing.T) {
	rdb := redis.NewClient(mustParseRedisURL(t))
	defer func() { _ = rdb.Close() }()

	email := fmt.Sprintf("livetest-leak-%s@example.com", uuid.NewString())
	registerData := doGraphQL(t, fmt.Sprintf(
		`mutation { register(email: %q, password: "password123456") { accessToken userId } }`, email), "")
	register := registerData["register"].(map[string]any)
	token := register["accessToken"].(string)
	userID := register["userId"].(string)

	channel := "realtime:user:" + userID

	conn := connectAndInit(t, token)

	subCtx, subCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer subCancel()
	wsSend(t, conn, subCtx, wsMsg{
		ID:      "sub-1",
		Type:    "subscribe",
		Payload: json.RawMessage(`{"query":"subscription { onNotification { id type message createdAt } }"}`),
	})

	// Give the resolver's goroutine a moment to actually issue the Redis
	// SUBSCRIBE before checking NUMSUB — subscribing is asynchronous
	// relative to the `subscribe` websocket message being accepted.
	subCount, err := waitForNumSub(t, rdb, channel, 1, 5*time.Second)
	if err != nil {
		t.Fatalf("expected exactly 1 redis subscriber on %s after opening the subscription, got count=%d: %v", channel, subCount, err)
	}
	t.Logf("confirmed %d live redis subscriber(s) on %s while connected", subCount, channel)

	// Disconnect without a clean `complete` — simulating a dropped client
	// (a closed tab, a lost connection), the more realistic and more
	// dangerous leak scenario than a graceful unsubscribe.
	if err := conn.CloseNow(); err != nil {
		t.Logf("CloseNow: %v (non-fatal, connection may already be closing)", err)
	}

	subCount, err = waitForNumSub(t, rdb, channel, 0, 10*time.Second)
	if err != nil {
		t.Fatalf("expected the redis subscription to be released after the client disconnected, but got count=%d after waiting: %v", subCount, err)
	}
	t.Logf("confirmed 0 redis subscribers on %s after disconnect — no leak", channel)
}

func mustParseRedisURL(t *testing.T) *redis.Options {
	t.Helper()
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatalf("parse %s: %v", redisURL, err)
	}
	return opts
}

// waitForNumSub polls `PUBSUB NUMSUB channel` until it reports want
// subscribers or timeout elapses, returning the last observed count and a
// non-nil error if it never reached want in time.
func waitForNumSub(t *testing.T, rdb *redis.Client, channel string, want int64, timeout time.Duration) (int64, error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last int64
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		counts, err := rdb.PubSubNumSub(ctx, channel).Result()
		cancel()
		if err != nil {
			return 0, fmt.Errorf("PUBSUB NUMSUB %s: %w", channel, err)
		}
		last = counts[channel]
		if last == want {
			return last, nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return last, fmt.Errorf("PUBSUB NUMSUB %s never reached %d within %s (last=%d)", channel, want, timeout, last)
}
