package realtime

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/cache"
	"github.com/Siddsharma25/skill-bridge-platform/backend/internal/platform/rabbitmq"
)

// fakePubSub is an in-memory stand-in for cache.PubSub — same reasoning as
// every other fake in this codebase's tests (fakePublisher, fakeMatchStore,
// ...): lets a test assert exactly what was published/subscribed without a
// real Redis instance.
type fakePubSub struct {
	published []publishCall
}

type publishCall struct {
	channel string
	message string
}

func (f *fakePubSub) Publish(_ context.Context, channel, message string) {
	f.published = append(f.published, publishCall{channel: channel, message: message})
}

func (f *fakePubSub) Subscribe(_ context.Context, _ string) (cache.Subscription, bool) {
	return nil, false
}

func TestBridgeHandler_RepublishesToUserChannel(t *testing.T) {
	ps := &fakePubSub{}
	bridge := NewBridge(ps, zap.NewNop())
	handler := bridge.Handler()

	notif := rabbitmq.RealtimeNotification{
		ID: "notif-1", UserID: "user-123", Type: rabbitmq.RealtimeNotificationTypeJobMatch,
		Message: "match!", CreatedAt: time.Now().UTC(), JobID: "job-1", Score: 0.5,
	}
	body, err := json.Marshal(notif)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if err := handler(context.Background(), body); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(ps.published) != 1 {
		t.Fatalf("expected exactly 1 publish, got %d", len(ps.published))
	}
	if want := UserChannel("user-123"); ps.published[0].channel != want {
		t.Errorf("expected channel %q, got %q", want, ps.published[0].channel)
	}
	var got rabbitmq.RealtimeNotification
	if err := json.Unmarshal([]byte(ps.published[0].message), &got); err != nil {
		t.Fatalf("republished message did not decode as RealtimeNotification: %v", err)
	}
	if got.UserID != "user-123" || got.JobID != "job-1" {
		t.Errorf("republished payload lost fields: %+v", got)
	}
}

func TestBridgeHandler_MalformedPayloadIsDroppedNotErrored(t *testing.T) {
	ps := &fakePubSub{}
	bridge := NewBridge(ps, zap.NewNop())
	handler := bridge.Handler()

	if err := handler(context.Background(), []byte("not json")); err != nil {
		t.Fatalf("expected a malformed payload to be logged and dropped, not returned as an error, got: %v", err)
	}
	if len(ps.published) != 0 {
		t.Fatalf("expected no publish for a malformed payload, got %d", len(ps.published))
	}
}

func TestBridgeHandler_MissingUserIDIsDroppedNotErrored(t *testing.T) {
	ps := &fakePubSub{}
	bridge := NewBridge(ps, zap.NewNop())
	handler := bridge.Handler()

	notif := rabbitmq.RealtimeNotification{ID: "notif-1", Type: rabbitmq.RealtimeNotificationTypeWelcome}
	body, _ := json.Marshal(notif)

	if err := handler(context.Background(), body); err != nil {
		t.Fatalf("expected a missing user_id to be logged and dropped, not returned as an error, got: %v", err)
	}
	if len(ps.published) != 0 {
		t.Fatalf("expected no publish when user_id is missing, got %d", len(ps.published))
	}
}

func TestUserChannel_Format(t *testing.T) {
	if got, want := UserChannel("abc-123"), "realtime:user:abc-123"; got != want {
		t.Errorf("UserChannel(%q) = %q, want %q", "abc-123", got, want)
	}
}
