// Package convex is a Go client for Convex (https://convex.dev).
//
// It calls queries, mutations and actions on a Convex deployment through the
// documented HTTP API (https://docs.convex.dev/http-api/). Results are not
// reactive: each call returns the function's value at that moment. The
// naming follows the official Python and Rust clients, so code moves easily
// between them.
//
//	client, err := convex.NewClient("https://happy-otter-123.convex.cloud")
//	if err != nil {
//		log.Fatal(err)
//	}
//	messages, err := client.Query(ctx, "messages:list", nil)
//	_, err = client.Mutation(ctx, "messages:send", map[string]any{
//		"author": "Ada",
//		"body":   "Hello from Go",
//	})
//
// # Values
//
// Arguments and results are Convex values. Results use nil, bool, float64,
// int64, string, []byte, []any and map[string]any. When encoding, int64
// becomes a Convex Int64 (a JavaScript bigint), while int, int32 and the
// other integer types become a Convex Float64 (a JavaScript number). Structs
// encode as objects using their json tags. DecodeInto, and the generic
// QueryAs, MutationAs and ActionAs helpers, copy results into structs.
//
// # Errors
//
// A function that throws a ConvexError returns a *ConvexError carrying its
// data. Other function failures return an *ExecutionError. HTTP level
// failures return an *HTTPError. Use errors.As to tell them apart.
package convex
