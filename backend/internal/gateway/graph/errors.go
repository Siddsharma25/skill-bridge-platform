package graph

// This file is hand-written and never touched by `make gen` (unlike
// schema.resolvers.go, whose method bodies gqlgen preserves but whose
// unrecognized top-level declarations it relocates into a commented-out
// block on every regeneration — see backend/CLAUDE.md). Keeping these
// small shared helpers here instead of in schema.resolvers.go means
// running gqlgen after adding a new field never risks commenting them out
// again.

import "google.golang.org/grpc/status"

// translateGRPCError unwraps a gRPC status error into a plain message
// GraphQL clients can show directly, instead of gqlgen's default
// "rpc error: code = ... desc = ..." wrapping leaking transport details.
func translateGRPCError(err error) error {
	if st, ok := status.FromError(err); ok {
		return &gqlError{msg: st.Message()}
	}
	return err
}

type gqlError struct{ msg string }

func (e *gqlError) Error() string { return e.msg }
