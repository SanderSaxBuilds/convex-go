package convex_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"

	convex "github.com/SanderSaxBuilds/convex-go"
)

func Example() {
	ctx := context.Background()
	client, err := convex.NewClient(os.Getenv("CONVEX_URL"))
	if err != nil {
		log.Fatal(err)
	}

	id, err := client.Mutation(ctx, "messages:send", map[string]any{
		"author": "Ada",
		"body":   "Hello from Go",
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("inserted", id)

	messages, err := client.Query(ctx, "messages:list", nil)
	if err != nil {
		log.Fatal(err)
	}
	for _, m := range messages.([]any) {
		fmt.Println(m.(map[string]any)["body"])
	}
}

func ExampleQueryAs() {
	type Message struct {
		ID     string `json:"_id"`
		Author string `json:"author"`
		Body   string `json:"body"`
	}
	client, err := convex.NewClient(os.Getenv("CONVEX_URL"))
	if err != nil {
		log.Fatal(err)
	}
	messages, err := convex.QueryAs[[]Message](context.Background(), client, "messages:list", nil)
	if err != nil {
		log.Fatal(err)
	}
	for _, m := range messages {
		fmt.Println(m.Author, m.Body)
	}
}

func ExampleConvexError() {
	client, err := convex.NewClient(os.Getenv("CONVEX_URL"))
	if err != nil {
		log.Fatal(err)
	}
	_, err = client.Mutation(context.Background(), "messages:send", map[string]any{"author": "Ada", "body": "..."})

	var cerr *convex.ConvexError
	switch {
	case errors.As(err, &cerr):
		// Thrown with `throw new ConvexError({code: "TOO_LONG", max: 100n})`.
		data := cerr.Data.(map[string]any)
		fmt.Println(data["code"], data["max"].(int64))
	case err != nil:
		log.Fatal(err)
	}
}

func ExampleClient_SetAdminAuth() {
	client, err := convex.NewClient(os.Getenv("CONVEX_URL"), convex.WithDeployKey(os.Getenv("CONVEX_DEPLOY_KEY")))
	if err != nil {
		log.Fatal(err)
	}
	// Deploy keys can call internal functions.
	stats, err := client.Query(context.Background(), "admin:stats", nil)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(stats)
}

func ExampleToJSON() {
	j, err := convex.EncodeJSON(map[string]any{
		"count": int64(1),     // Int64, a bigint in JavaScript
		"limit": 10,           // Float64, a number in JavaScript
		"data":  []byte("hi"), // Bytes, an ArrayBuffer in JavaScript
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(j))
	// Output: {"count":{"$integer":"AQAAAAAAAAA="},"data":{"$bytes":"aGk="},"limit":10}
}
