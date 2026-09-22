package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog" // <-- NEW: Structured Logging
	"os"
	"strings"

	"flashcart/backend/internal/inventory"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

var db *dynamodb.Client
var productsTable string
var ordersTable string

func handler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	path := req.RawPath
	method := req.RequestContext.HTTP.Method

	// Structured logging: This prints a JSON log every time someone visits the API
	slog.Info("Incoming request", 
		slog.String("method", method), 
		slog.String("path", path),
		slog.String("request_id", req.RequestContext.RequestID),
	)

	if strings.HasPrefix(path, "/admin/products") && method == "POST" {
		return handleAdminSeed(ctx, req)
	} else if strings.HasPrefix(path, "/products/") && method == "GET" {
		return handleGetProduct(ctx, req)
	} else if strings.HasPrefix(path, "/orders") && method == "POST" {
		return handleCreateOrder(ctx, req)
	} else if strings.HasPrefix(path, "/orders/") && method == "GET" {
		return handleGetOrder(ctx, req) // <-- NEW: The missing endpoint route!
	}

	return buildResponse(404, map[string]string{"error": "Route not found"})
}

func handleAdminSeed(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	db.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(productsTable),
		Item: map[string]types.AttributeValue{
			"productId": &types.AttributeValueMemberS{Value: "FLASH-TV-001"},
			"stock":     &types.AttributeValueMemberN{Value: "500"},
		},
	})
	return buildResponse(201, map[string]string{"message": "Sale Seeded!"})
}

func handleGetProduct(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	parts := strings.Split(req.RawPath, "/")
	productID := parts[len(parts)-1]

	result, _ := db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(productsTable),
		Key:       map[string]types.AttributeValue{"productId": &types.AttributeValueMemberS{Value: productID}},
	})

	if result.Item == nil {
		return buildResponse(404, map[string]string{"error": "Product not found"})
	}

	stock := result.Item["stock"].(*types.AttributeValueMemberN).Value
	return buildResponse(200, map[string]string{"productId": productID, "stock": stock})
}

func handleCreateOrder(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	idempotencyKey := req.Headers["idempotency-key"]
	if idempotencyKey == "" {
		return buildResponse(400, map[string]string{"error": "Missing Idempotency-Key header"})
	}

	order := inventory.Order{
		OrderID:   idempotencyKey,
		ProductID: "FLASH-TV-001",
		Qty:       1,
	}

	err := inventory.Reserve(ctx, db, productsTable, ordersTable, order)

	if err != nil {
		var tce *types.TransactionCanceledException
		if errors.As(err, &tce) {
			if *tce.CancellationReasons[0].Code == "ConditionalCheckFailed" {
				return buildResponse(409, map[string]string{"error": "SOLD_OUT"})
			}
			if *tce.CancellationReasons[1].Code == "ConditionalCheckFailed" {
				return buildResponse(200, map[string]string{"message": "Order already processed (Idempotent replay)"})
			}
		}
		slog.Error("Database error", slog.String("error", err.Error())) // Log real errors!
		return buildResponse(500, map[string]string{"error": "Internal Server Error"})
	}

	return buildResponse(201, map[string]string{"message": "Order placed successfully!"})
}

// NEW: The function to let a user check their order status!
func handleGetOrder(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	parts := strings.Split(req.RawPath, "/")
	orderID := parts[len(parts)-1]

	result, _ := db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(ordersTable),
		Key:       map[string]types.AttributeValue{"orderId": &types.AttributeValueMemberS{Value: orderID}},
	})

	if result.Item == nil {
		return buildResponse(404, map[string]string{"error": "Order not found"})
	}

	status := result.Item["status"].(*types.AttributeValueMemberS).Value
	return buildResponse(200, map[string]string{"orderId": orderID, "status": status})
}

func buildResponse(statusCode int, body map[string]string) (events.APIGatewayV2HTTPResponse, error) {
	jsonBody, _ := json.Marshal(body)
	return events.APIGatewayV2HTTPResponse{
		StatusCode: statusCode,
		Headers:    map[string]string{"Content-Type": "application/json"},
		Body:       string(jsonBody),
	}, nil
}

func main() {
	// Set up our logger to output as JSON so AWS CloudWatch can read it easily
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	productsTable = os.Getenv("PRODUCTS_TABLE")
	ordersTable = os.Getenv("ORDERS_TABLE")

	// We already have our "Cold-start performance" optimization here
	cfg, _ := config.LoadDefaultConfig(context.TODO())
	db = dynamodb.NewFromConfig(cfg)

	lambda.Start(handler)
}