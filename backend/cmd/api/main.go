package main

import (
	"context"
	"encoding/json"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
)

// This is the function AWS calls when someone hits your API
func handler(ctx context.Context, request events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	
	// Create a simple JSON response
	body, _ := json.Marshal(map[string]string{
		"message": "Hello from FlashCart!",
	})

	return events.APIGatewayV2HTTPResponse{
		StatusCode: 200,
		Headers: map[string]string{
			"Content-Type": "application/json",
		},
		Body: string(body),
	}, nil
}

func main() {
	// Starts the Lambda function
	lambda.Start(handler)
}