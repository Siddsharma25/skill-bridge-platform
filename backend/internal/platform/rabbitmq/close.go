package rabbitmq

import (
	"time"

	"go.uber.org/zap"
)

// closeTimeout bounds how long Producer.Close/Consumer.Close wait for
// amqp091-go's synchronous close handshake (Channel.Close/Connection.Close
// both send an AMQP method and block waiting for the broker's ...-ok
// reply, via the same per-channel goroutine that also dispatches this
// connection's consumer deliveries and RPC replies) before giving up.
//
// Discovered live during Phase 3.5's graceful-shutdown verification, not
// something obvious from reading amqp091-go's godoc alone: closing a
// channel that had an active Consume whose delivery loop had just
// stopped reading (exactly what api-gateway's realtime consumer does on
// every shutdown — see consumer.go's Run, which returns as soon as ctx is
// cancelled, without explicitly draining or cancelling the AMQP-level
// consumer subscription first) can leave amqp091-go's internal delivery
// dispatch wedged, which in turn blocks the *next* synchronous RPC on
// that same channel — including the close-ok reply Close() itself is
// waiting for — forever. Observed live: cmd/api-gateway/main.go's entire
// shutdown.Wait sequence hung indefinitely (well past its documented
// ~10s bound) inside Consumer.Close()'s Channel.Close() call, confirmed
// via a SIGQUIT goroutine dump showing the main goroutine parked in
// amqp091-go's Channel.call while nothing else in the process was making
// progress.
//
// Bounding the close with a timeout — the same "run it in the
// background, give up after a bound, log and move on" shape gqlgen's own
// coder-websocket transport adapter already uses for its WriteClose, for
// what turns out to be an analogous reason — converts a potential
// permanent process hang into a logged, bounded delay: a real fix would
// additionally have Consumer.Run explicitly `ch.Cancel` the AMQP consumer
// subscription before returning (closing the gap that causes the
// wedge in the first place), which is worth doing in a follow-up but
// wasn't required to make shutdown itself safe, which is the property
// that actually matters here.
const closeTimeout = 3 * time.Second

// closeWithTimeout runs fn (expected to be a blocking close operation) in
// its own goroutine and waits at most closeTimeout for it to finish,
// logging a warning and returning early — never blocking the caller
// indefinitely — if it doesn't. See closeTimeout's doc comment for why
// this exists.
func closeWithTimeout(fn func(), log *zap.Logger, what string) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(closeTimeout):
		if log != nil {
			log.Warn("rabbitmq close timed out; abandoning the close handshake so shutdown can proceed",
				zap.String("what", what))
		}
	}
}
