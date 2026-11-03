package main

import (
	"context"
	"testing"

	"github.com/aws/aws-lambda-go/events"
)

func request(method, path string, headers map[string]string) events.APIGatewayV2HTTPRequest {
	req := events.APIGatewayV2HTTPRequest{RawPath: path, Headers: headers}
	req.RequestContext.HTTP.Method = method
	return req
}

// None of these cases reach DynamoDB, so the nil client is never used.
func TestHandlerRejectsRequestsBeforeTouchingDynamo(t *testing.T) {
	cases := []struct {
		name    string
		req     events.APIGatewayV2HTTPRequest
		want    int
		wantMsg string
	}{
		{"unknown route", request("GET", "/nope", nil), 404, "Route not found"},
		{"empty product id", request("GET", "/products/", nil), 404, "Product not found"},
		{"empty order id", request("GET", "/orders/", nil), 404, "Order not found"},
		{"order without idempotency key", request("POST", "/orders", nil), 400, "Missing Idempotency-Key header"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, err := handler(context.Background(), c.req)
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != c.want {
				t.Errorf("status = %d, want %d (body %s)", resp.StatusCode, c.want, resp.Body)
			}
			if want := `{"error":"` + c.wantMsg + `"}`; resp.Body != want {
				t.Errorf("body = %s, want %s", resp.Body, want)
			}
		})
	}
}
