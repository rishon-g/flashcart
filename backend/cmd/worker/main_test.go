package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"testing"

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
		{"no new image", `{"dynamodb":{}}`, "", ""},
		{"not json", `not json`, "", ""},
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
	if _, ok := got["_aws"]; !ok {
		t.Error("missing _aws metadata block")
	}
}
