package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

func TestParsePipeMessage(t *testing.T) {
	cases := []struct {
		name        string
		body        string
		wantOrder   string
		wantProduct string
	}{
		{"stream record", `{"dynamodb":{"NewImage":{"orderId":{"S":"o-1"},"productId":{"S":"FLASH-TV-001"}}}}`, "o-1", "FLASH-TV-001"},
		{"missing product", `{"dynamodb":{"NewImage":{"orderId":{"S":"o-1"}}}}`, "o-1", ""},
		{"missing order", `{"dynamodb":{"NewImage":{"productId":{"S":"FLASH-TV-001"}}}}`, "", "FLASH-TV-001"},
		{"numeric order id", `{"dynamodb":{"NewImage":{"orderId":{"N":"1"},"productId":{"S":"FLASH-TV-001"}}}}`, "", "FLASH-TV-001"},
		{"no new image", `{"dynamodb":{}}`, "", ""},
		{"not json", `not json`, "", ""},
		{"array of records", `[{"dynamodb":{"NewImage":{"orderId":{"S":"o-1"},"productId":{"S":"FLASH-TV-001"}}}}]`, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			order, product := parsePipeMessage(c.body)
			if order != c.wantOrder || product != c.wantProduct {
				t.Errorf("got (%q, %q), want (%q, %q)", order, product, c.wantOrder, c.wantProduct)
			}
		})
	}
}

// Malformed records are reported before any DynamoDB call, so the nil client is never used.
func TestHandlerReportsMalformedMessagesAsBatchFailures(t *testing.T) {
	event := events.SQSEvent{Records: []events.SQSMessage{
		{MessageId: "m-1", Body: "not json"},
		{MessageId: "m-2", Body: `{"dynamodb":{"NewImage":{"orderId":{"S":"o-1"}}}}`},
		{MessageId: "m-3", Body: `{"dynamodb":{"NewImage":{"productId":{"S":"FLASH-TV-001"}}}}`},
	}}

	resp, err := handler(context.Background(), event)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.BatchItemFailures) != 3 {
		t.Fatalf("got %d batch failures, want 3", len(resp.BatchItemFailures))
	}
	for i, id := range []string{"m-1", "m-2", "m-3"} {
		if got := resp.BatchItemFailures[i].ItemIdentifier; got != id {
			t.Errorf("failure %d = %q, want %q", i, got, id)
		}
	}
}

func TestHandlerWithEmptyBatchReportsNoFailures(t *testing.T) {
	resp, err := handler(context.Background(), events.SQSEvent{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.BatchItemFailures) != 0 {
		t.Errorf("got %d batch failures for an empty batch, want 0", len(resp.BatchItemFailures))
	}
}

func TestIsAlreadyProcessed(t *testing.T) {
	if isAlreadyProcessed(nil) {
		t.Error("nil error should not count as already processed")
	}
	if isAlreadyProcessed(errors.New("boom")) {
		t.Error("an unrelated error should not count as already processed")
	}
	if !isAlreadyProcessed(&types.ConditionalCheckFailedException{}) {
		t.Error("ConditionalCheckFailedException should count as already processed")
	}
	tce := &types.TransactionCanceledException{CancellationReasons: []types.CancellationReason{{Code: aws.String("ConditionalCheckFailed")}}}
	if !isAlreadyProcessed(tce) {
		t.Error("a cancelled transaction whose first reason is a failed condition should count")
	}
	tce = &types.TransactionCanceledException{CancellationReasons: []types.CancellationReason{{Code: aws.String("None")}, {Code: aws.String("ConditionalCheckFailed")}}}
	if isAlreadyProcessed(tce) {
		t.Error("only the first cancellation reason (the order update) should count")
	}
	if isAlreadyProcessed(&types.TransactionCanceledException{}) {
		t.Error("a cancelled transaction with no cancellation reasons should not count")
	}
}

// the SDK returns service exceptions wrapped in an operation error, so the check must unwrap
func TestIsAlreadyProcessedUnwrapsSDKErrors(t *testing.T) {
	wrapped := fmt.Errorf("operation error DynamoDB: UpdateItem: %w", &types.ConditionalCheckFailedException{})
	if !isAlreadyProcessed(wrapped) {
		t.Error("a wrapped ConditionalCheckFailedException should count as already processed")
	}
	tce := &types.TransactionCanceledException{CancellationReasons: []types.CancellationReason{{Code: aws.String("ConditionalCheckFailed")}}}
	if !isAlreadyProcessed(fmt.Errorf("operation error DynamoDB: TransactWriteItems: %w", tce)) {
		t.Error("a wrapped cancelled transaction whose first reason is a failed condition should count")
	}
}

func TestLogEMFMetricEmitsValidEMFJSON(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	logEMFMetric("OrderConfirmed", 3)
	os.Stdout = orig
	w.Close()
	out, _ := io.ReadAll(r)

	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if got["OrderConfirmed"] != float64(3) {
		t.Errorf("OrderConfirmed = %v, want 3", got["OrderConfirmed"])
	}
	meta, ok := got["_aws"].(map[string]any)
	if !ok {
		t.Fatal("missing _aws metadata block")
	}
	directives, _ := meta["CloudWatchMetrics"].([]any)
	if len(directives) != 1 {
		t.Fatalf("got %d CloudWatchMetrics directives, want 1", len(directives))
	}
	directive, _ := directives[0].(map[string]any)
	if directive["Namespace"] != "FlashCart" {
		t.Errorf("Namespace = %v, want FlashCart", directive["Namespace"])
	}
	metrics, _ := directive["Metrics"].([]any)
	if len(metrics) != 1 || metrics[0].(map[string]any)["Name"] != "OrderConfirmed" {
		t.Errorf("Metrics = %v, want one metric named OrderConfirmed", metrics)
	}
}
