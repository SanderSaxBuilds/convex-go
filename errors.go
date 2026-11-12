package convex

import (
	"fmt"
	"strings"
)

// ConvexError is an application error thrown by a Convex function with
// `throw new ConvexError(data)`. Unlike other errors thrown in Convex
// functions, ConvexErrors reach the client with their data intact, so they
// can be inspected:
//
//	var cerr *convex.ConvexError
//	if errors.As(err, &cerr) {
//		fmt.Println(cerr.Data)
//	}
//
// Data holds the decoded value in the same Go types the client returns for
// function results, and DecodeInto can copy it into a struct.
type ConvexError struct {
	// Message is the errorMessage the server sent.
	Message string
	// Data is the value the function passed to the ConvexError constructor.
	Data any
	// LogLines are the lines the function logged before it failed. They are
	// only sent by development deployments.
	LogLines []string
}

func (e *ConvexError) Error() string {
	if e.Message == "" {
		return "convex: ConvexError"
	}
	return e.Message
}

// ExecutionError is any other error raised while a Convex function ran, for
// example an uncaught exception, a failed argument validator or a call to a
// function that does not exist. The server redacts the details of these
// errors on production deployments.
type ExecutionError struct {
	Message  string
	LogLines []string
}

func (e *ExecutionError) Error() string {
	return e.Message
}

// HTTPError is returned when the deployment answers with an HTTP status that
// is not a function result, such as 400 for a malformed request, 401 for a
// rejected auth token or 503 while the deployment is unavailable. Code and
// Message come from the JSON body when the server sent one.
type HTTPError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *HTTPError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "convex: HTTP %d", e.StatusCode)
	if e.Code != "" {
		b.WriteString(" " + e.Code)
	}
	if e.Message != "" {
		b.WriteString(": " + e.Message)
	}
	return b.String()
}

// Temporary reports whether the request may succeed if it is sent again.
// It is true for 5xx responses.
func (e *HTTPError) Temporary() bool {
	return e.StatusCode >= 500 && e.StatusCode != statusFunctionFailed
}

// ResponseError is returned when a 2xx response, or a function failure
// response, cannot be understood.
type ResponseError struct {
	StatusCode int
	Msg        string
}

func (e *ResponseError) Error() string {
	return fmt.Sprintf("convex: unexpected response (HTTP %d): %s", e.StatusCode, e.Msg)
}

// transportError marks network failures so the retry loop can tell them
// apart. It unwraps to the error from the HTTP client.
type transportError struct {
	err error
}

func (e *transportError) Error() string { return "convex: request failed: " + e.err.Error() }
func (e *transportError) Unwrap() error { return e.err }
