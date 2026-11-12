# convex-go

A Go client for [Convex](https://convex.dev). It runs queries, mutations and actions on a Convex deployment over the [HTTP API](https://docs.convex.dev/http-api/), keeps every Convex type intact on the way in and out, and has no dependencies outside the standard library.

The method names and behavior follow the official [Python](https://github.com/convex/convex-py) and [Rust](https://github.com/convex/convex-rs) clients, so code reads the same across the three.

Results are not reactive. Each call returns the function's value at that moment, like `ConvexHttpClient` in the JavaScript client. Live subscriptions need the WebSocket sync protocol, which this client does not speak.

## Install

```sh
go get github.com/SanderSaxBuilds/convex-go
```

Go 1.22 or newer.

## Quick start

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	convex "github.com/SanderSaxBuilds/convex-go"
)

func main() {
	ctx := context.Background()

	client, err := convex.NewClient(os.Getenv("CONVEX_URL"))
	if err != nil {
		log.Fatal(err)
	}

	_, err = client.Mutation(ctx, "messages:send", map[string]any{
		"author": "Ada",
		"body":   "Hello from Go",
	})
	if err != nil {
		log.Fatal(err)
	}

	messages, err := client.Query(ctx, "messages:list", nil)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(messages)
}
```

The deployment URL is on the Settings page of your deployment in the Convex dashboard, and `npx convex dev` writes it to `.env.local` as `CONVEX_URL`. It looks like `https://happy-otter-123.convex.cloud`. For the open-source backend it is usually `http://127.0.0.1:3210`.

## Calls

Every call takes a `context.Context`, the function path and its arguments, and returns the result as `any`.

```go
client.Query(ctx, "messages:list", args)     // POST /api/query
client.Mutation(ctx, "messages:send", args)  // POST /api/mutation
client.Action(ctx, "ai:summarize", args)     // POST /api/action
client.Function(ctx, "messages:list", args)  // POST /api/run/messages/list
```

Function paths are written `file:export`, with folders separated by slashes: `"messages:list"` is the `list` export of `convex/messages.ts`, and `"admin/users:remove"` is `remove` in `convex/admin/users.ts`. A path without a colon refers to the default export.

`Function` runs a query, mutation or action without saying which, through the `/api/run/{functionIdentifier}` endpoint. Use it when the type is not known ahead of time. `RunPath` shows the URL form it uses.

Arguments can be `nil`, a `map[string]any` (or any map with string keys) or a struct. They must form a Convex object, since Convex functions take named arguments.

To get a typed result, use the generic helpers or `DecodeInto`:

```go
type Message struct {
	ID      string  `json:"_id"`
	Created float64 `json:"_creationTime"`
	Author  string  `json:"author"`
	Body    string  `json:"body"`
}

messages, err := convex.QueryAs[[]Message](ctx, client, "messages:list", nil)

// or
v, err := client.Query(ctx, "messages:list", nil)
var out []Message
err = convex.DecodeInto(v, &out)
```

`MutationAs`, `ActionAs` and `FunctionAs` work the same way. Struct fields are matched by their `json` tags, using the same rules as `encoding/json`.

## Values

Convex has eight value types. This is how they map to Go:

| Convex   | JavaScript    | Go result        | Go argument                                   |
| -------- | ------------- | ---------------- | --------------------------------------------- |
| Null     | `null`        | `nil`            | `nil`, nil pointers, nil maps and slices      |
| Int64    | `bigint`      | `int64`          | `int64`, `uint64`, named types of kind int64  |
| Float64  | `number`      | `float64`        | `float64`, `float32`, `int`, `int32`, `uint`… |
| Boolean  | `boolean`     | `bool`           | `bool`                                        |
| String   | `string`      | `string`         | `string`, `encoding.TextMarshaler`            |
| Bytes    | `ArrayBuffer` | `[]byte`         | `[]byte`, `[N]byte`                           |
| Array    | `Array`       | `[]any`          | slices and arrays                             |
| Object   | `Object`      | `map[string]any` | maps with string keys, structs                |

The one rule to remember is about integers. An `int64` becomes a Convex Int64, which arrives in JavaScript as a `bigint` and matches `v.int64()`. Every other Go integer type (`int`, `int32`, `uint16` and so on) becomes a Convex Float64, which arrives as a plain `number` and matches `v.number()`. This is the same choice the Python client makes for Python `int`, and it means `map[string]any{"limit": 10}` does what you expect. Those integers must fit in the range a float64 holds exactly, -2^53 to 2^53. Values outside it return an error instead of losing precision.

```go
client.Mutation(ctx, "counters:add", map[string]any{
	"by":    1,              // number
	"total": int64(1 << 60), // bigint
	"data":  []byte{0, 1},   // ArrayBuffer
})
```

On the wire the client uses the Convex encoded JSON format, the same one the official JavaScript and Python HTTP clients send:

| Value                  | JSON                                         |
| ---------------------- | -------------------------------------------- |
| `int64(1)`             | `{"$integer": "AQAAAAAAAAA="}` (8 bytes, little-endian, base64) |
| `[]byte("hi")`         | `{"$bytes": "aGk="}` (standard base64)      |
| `math.Inf(1)`          | `{"$float": "AAAAAAAA8H8="}` (8 bytes, little-endian, base64) |
| NaN, -Inf, -0          | `{"$float": ...}` in the same way            |
| other floats           | plain JSON numbers                           |

The encoders are exported for use outside the client, for example when writing files for `npx convex import`:

```go
convex.ToJSON(v)       // Go value to a JSON tree, like convex_to_json in Python
convex.FromJSON(j)     // JSON tree to Go values, like json_to_convex in Python
convex.EncodeJSON(v)   // Go value to JSON bytes
convex.DecodeJSON(b)   // JSON bytes to Go values
```

Object field names follow the Convex rules: ASCII without control characters, at most 1024 characters, and not starting with `$`.

## Errors

Calls return one of these error types. Use `errors.As` to check which:

| Type              | When                                                                  |
| ----------------- | --------------------------------------------------------------------- |
| `*ConvexError`    | The function threw `new ConvexError(data)`. `Data` holds the value.   |
| `*ExecutionError` | Any other failure inside the function: an uncaught exception, an argument validator, a function that does not exist. |
| `*HTTPError`      | The deployment answered with a non-function HTTP error, such as 400 for a bad request or 401 for a rejected token. `Code` and `Message` come from the response body. |
| `*ValueError`     | An argument or result could not be converted. `Path` shows where.     |
| `*ResponseError`  | The response could not be understood.                                 |

Network failures unwrap to the `*url.Error` from `net/http`, and a canceled or expired context returns `context.Canceled` or `context.DeadlineExceeded`.

```go
_, err := client.Mutation(ctx, "messages:send", args)

var cerr *convex.ConvexError
if errors.As(err, &cerr) {
	// throw new ConvexError({ code: "TOO_LONG", max: 100n })
	var data struct {
		Code string `json:"code"`
		Max  int64  `json:"max"`
	}
	_ = convex.DecodeInto(cerr.Data, &data)
	fmt.Println(data.Code, data.Max)
}
```

Both `ConvexError` and `ExecutionError` carry the function's `LogLines` when the deployment sends them, which development deployments do.

## Auth

For a signed-in user, pass the JWT from your auth provider (Clerk, Auth0, Convex Auth or any OpenID Connect provider):

```go
client.SetAuth(token)          // Authorization: Bearer <token>
client.ClearAuth()
```

Call `SetAuth` again whenever the token is refreshed. The client is safe to share between goroutines, and the new token applies to every later call.

For server-side jobs that need to call internal functions, use a deploy key from the deployment settings page:

```go
client, err := convex.NewClient(url, convex.WithDeployKey(os.Getenv("CONVEX_DEPLOY_KEY")))
// or
client.SetAdminAuth(deployKey)  // Authorization: Convex <key>
```

To check how a function behaves for a given user, an admin can act as that user:

```go
client.SetAdminAuthActingAs(deployKey, map[string]any{
	"subject": "user-42",
	"issuer":  "https://auth.example.com",
})
```

Deploy keys grant full access to the deployment. Keep them on servers.

## Retries

Queries are sent again after a network error or a 5xx response, three times by default, waiting a random time between zero and `min(100ms * 2^attempt, 2s)` before each retry. Queries are read-only, so this is always safe.

Mutations, actions and `Function` calls are never retried, because sending them twice could apply their side effects twice. Retry them in your own code if the function is idempotent.

A function that ran and failed is not retried either. Only transport and server availability problems are.

## Options

```go
convex.NewClient(url,
	convex.WithHTTPClient(&http.Client{Timeout: 10 * time.Second}),
	convex.WithAuth(token),
	convex.WithDeployKey(key),
	convex.WithQueryRetries(5),
	convex.WithBackoff(200*time.Millisecond, 5*time.Second),
	convex.WithLogHandler(func(path, line string) { log.Printf("[%s] %s", path, line) }),
	convex.WithWarningHandler(func(state, msg string) { log.Printf("%s: %s", state, msg) }),
)
```

`WithLogHandler` receives the `console.log` output of each function, which development deployments include with every response. It plays the part of `set_debug` in the Python client. `WithWarningHandler` receives deprecation notices from the deployment. Without it they are printed once each with the `log` package.

The client sends `Convex-Client: go-<version>` with every request so the deployment can tell it apart from other clients.

## How it compares

| Python (`ConvexClient`)        | Rust (`ConvexClient`)          | Go                                   |
| ------------------------------ | ------------------------------ | ------------------------------------ |
| `ConvexClient(url)`            | `ConvexClient::new(url)`       | `convex.NewClient(url)`              |
| `query(name, args)`            | `query(name, args)`            | `Query(ctx, name, args)`             |
| `mutation(name, args)`         | `mutation(name, args)`         | `Mutation(ctx, name, args)`          |
| `action(name, args)`           | `action(name, args)`           | `Action(ctx, name, args)`            |
| `set_auth(token)`              | `set_auth(Some(token))`        | `SetAuth(token)`                     |
| `clear_auth()`                 | `set_auth(None)`               | `ClearAuth()`                        |
| `set_admin_auth(key)`          | `set_admin_auth(key, None)`    | `SetAdminAuth(key)`                  |
| `ConvexError(message, data)`   | `FunctionResult::ConvexError`  | `*ConvexError{Message, Data}`        |
| `ConvexExecutionError`         | `FunctionResult::ErrorMessage` | `*ExecutionError`                    |
| `ConvexInt64(n)`               | `Value::Int64(n)`              | `int64(n)`                           |
| `subscribe(name, args)`        | `subscribe(name, args)`        | not available over HTTP              |

## Tests

```sh
go vet ./...
go test ./...
```

The unit tests run against `httptest` servers that answer with response bodies shaped like the HTTP API documentation and the open-source backend's own response types. Several bodies were captured from a real local backend. The value tests include a port of `test_values.py` from the Python client, so the two agree on the wire format.

### Integration tests

The integration tests run against a real deployment with the functions in `testdata/convex-app` deployed to it. With the [open-source backend](https://github.com/convex/convex-backend) from its releases page:

```sh
# 1. Start the backend
./convex-local-backend --instance-name convex-go-test --instance-secret <64 hex chars>

# 2. Make an admin key for it
./convex-local-backend keygen admin-key --instance-name convex-go-test --instance-secret <same secret>

# 3. Deploy the test functions
cd testdata/convex-app
npm install
CONVEX_SELF_HOSTED_URL=http://127.0.0.1:3210 CONVEX_SELF_HOSTED_ADMIN_KEY=<admin key> \
  npx convex deploy --typecheck disable --codegen disable -y

# 4. Run the tests
cd ../..
CONVEX_URL=http://127.0.0.1:3210 CONVEX_ADMIN_KEY=<admin key> go test -tags integration ./...
```

They also work against a cloud dev deployment, with its URL and a deploy key.

## License

MIT, see [LICENSE](LICENSE).
