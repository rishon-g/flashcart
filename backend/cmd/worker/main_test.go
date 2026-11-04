package main

import "testing"

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
