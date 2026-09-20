package main

import (
	"context"
	"encoding/json"
	"errors"
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

// Global variables for our database client and table names
var db *dynamodb.Client
var productsTable string
var ordersTable string

// ---------------------------------------------------------
// 1. THE ROUTER 
// ---------------------------------------------------------
func handler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	path := req.RawPath
	method := req.RequestContext.HTTP.Method

	// Check the URL and Method, and route to the correct function!
	if strings.HasPrefix(path, "/admin/products") && method == "POST" {
		return handleAdminSeed(ctx, req)
	} else if strings.HasPrefix(path, "/products/") && method == "GET" {
		return handleGetProduct(ctx, req)
	} else if strings.HasPrefix(path, "/orders") && method == "POST" {
		return handleCreateOrder(ctx, req)
	}

	// If they typed a bad URL, return a 404 (Not Found)
	return buildResponse(404, map[string]string{"error": "Route not found"})
}

// ---------------------------------------------------------
// 2. THE ENDPOINTS
// ---------------------------------------------------------

// POST /admin/products - Creates a product with 500 stock
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

// GET /products/{id} - Returns the current stock
func handleGetProduct(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	// Extract the ID from the URL (e.g., /products/FLASH-TV-001)
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

// POST /orders - THE BUY BUTTON
func handleCreateOrder(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	idempotencyKey := req.Headers["idempotency-key"]
	if idempotencyKey == "" {
		// 400 Bad Request: The frontend forgot to send the ID!
		return buildResponse(400, map[string]string{"error": "Missing Idempotency-Key header"})
	}

	order := inventory.Order{
		OrderID:   idempotencyKey,
		ProductID: "FLASH-TV-001", // Hardcoded for the flash sale
		Qty:       1,
	}

	// Call the transaction function you wrote in Phase 2!
	err := inventory.Reserve(ctx, db, productsTable, ordersTable, order)

	if err != nil {
		// MAGICAL ERROR PARSING (See Explanation Below!)
		var tce *types.TransactionCanceledException
		if errors.As(err, &tce) {
			// Reason 0 is our Stock Check. Reason 1 is our Idempotency Check.
			if *tce.CancellationReasons[0].Code == "ConditionalCheckFailed" {
				return buildResponse(409, map[string]string{"error": "SOLD_OUT"})
			}
			if *tce.CancellationReasons[1].Code == "ConditionalCheckFailed" {
				return buildResponse(200, map[string]string{"message": "Order already processed (Idempotent replay)"})
			}
		}
		// Some other database error
		return buildResponse(500, map[string]string{"error": "Internal Server Error"})
	}

	// 201 Created: The order succeeded!
	return buildResponse(201, map[string]string{"message": "Order placed successfully!"})
}

// Helper function to format JSON responses easily
func buildResponse(statusCode int, body map[string]string) (events.APIGatewayV2HTTPResponse, error) {
	jsonBody, _ := json.Marshal(body)
	return events.APIGatewayV2HTTPResponse{
		StatusCode: statusCode,
		Headers:    map[string]string{"Content-Type": "application/json"},
		Body:       string(jsonBody),
	}, nil
}

// ---------------------------------------------------------
// 3. INITIALIZATION
// ---------------------------------------------------------
func main() {
	// Grab the database names from the sticky notes we left in CDK!
	productsTable = os.Getenv("PRODUCTS_TABLE")
	ordersTable = os.Getenv("ORDERS_TABLE")

	// Connect to real AWS DynamoDB
	cfg, _ := config.LoadDefaultConfig(context.TODO())
	db = dynamodb.NewFromConfig(cfg)

	// Start listening for web traffic
	lambda.Start(handler)
}