package main

import (
	"errors"
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
