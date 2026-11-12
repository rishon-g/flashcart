package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
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
		{"nested product path", request("GET", "/products/FLASH-TV-001/extra", nil), 404, "Product not found"},
		{"empty order id", request("GET", "/orders/", nil), 404, "Order not found"},
		{"nested order path", request("GET", "/orders/o-1/extra", nil), 404, "Order not found"},
		{"order without idempotency key", request("POST", "/orders", nil), 400, "Missing Idempotency-Key header"},
		{"wrong method on products", request("POST", "/products/FLASH-TV-001", nil), 404, "Route not found"},
		{"get orders without an id segment", request("GET", "/orders", nil), 404, "Route not found"},
		{"post to a path that only starts with /orders", request("POST", "/ordersfoo", map[string]string{"idempotency-key": "k"}), 404, "Route not found"},
		{"post to a path that only starts with /admin/products", request("POST", "/admin/products/extra", nil), 404, "Route not found"},
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

func TestAdminSeedRequiresTheToken(t *testing.T) {
	adminToken = "s3cret"
	defer func() { adminToken = "" }()

	for name, headers := range map[string]map[string]string{
		"missing": nil,
		"wrong":   {"x-admin-token": "nope"},
	} {
		resp, _ := handler(context.Background(), request("POST", "/admin/products", headers))
		if resp.StatusCode != 401 {
			t.Errorf("%s token: status = %d, want 401", name, resp.StatusCode)
		}
	}
}

func TestAdminSeedRejectsEveryCallerWhenNoTokenIsConfigured(t *testing.T) {
	adminToken = ""

	// an empty header must not match an unset token
	resp, _ := handler(context.Background(), request("POST", "/admin/products", map[string]string{"x-admin-token": ""}))
	if resp.StatusCode != 401 {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestPreflightAllowsTheCustomHeaders(t *testing.T) {
	resp, _ := handler(context.Background(), request("OPTIONS", "/orders", nil))
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	allowed := resp.Headers["Access-Control-Allow-Headers"]
	for _, h := range []string{"Idempotency-Key", "X-Admin-Token"} {
		if !strings.Contains(allowed, h) {
			t.Errorf("Access-Control-Allow-Headers %q is missing %s", allowed, h)
		}
	}
}

func TestBuildResponseReturnsJSONWithCORSHeaders(t *testing.T) {
	resp, err := buildResponse(409, map[string]string{"error": "SOLD_OUT"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 409 {
		t.Errorf("status = %d, want 409", resp.StatusCode)
	}
	if got := resp.Headers["Content-Type"]; got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if got := resp.Headers["Access-Control-Allow-Origin"]; got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want *", got)
	}
	if resp.Body != `{"error":"SOLD_OUT"}` {
		t.Errorf("body = %s", resp.Body)
	}
}

func TestNotFoundResponseStillCarriesCORSHeaders(t *testing.T) {
	resp, _ := handler(context.Background(), request("GET", "/nope", nil))
	if resp.StatusCode != 404 {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if got := resp.Headers["Access-Control-Allow-Origin"]; got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want * so the browser can read the 404", got)
	}
}

func TestLogEMFMetricEmitsValidEMFJSON(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	logEMFMetric("OrderCreated", 1)
	os.Stdout = orig
	w.Close()
	out, _ := io.ReadAll(r)

	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if got["OrderCreated"] != float64(1) {
		t.Errorf("OrderCreated = %v, want 1", got["OrderCreated"])
	}
	aws, ok := got["_aws"].(map[string]any)
	if !ok {
		t.Fatal("missing _aws metadata block")
	}
	directives, _ := aws["CloudWatchMetrics"].([]any)
	if len(directives) != 1 {
		t.Fatalf("got %d CloudWatchMetrics directives, want 1", len(directives))
	}
	directive, _ := directives[0].(map[string]any)
	if directive["Namespace"] != "FlashCart" {
		t.Errorf("Namespace = %v, want FlashCart", directive["Namespace"])
	}
	metrics, _ := directive["Metrics"].([]any)
	if len(metrics) != 1 || metrics[0].(map[string]any)["Name"] != "OrderCreated" {
		t.Errorf("Metrics = %v, want one metric named OrderCreated", metrics)
	}
}
