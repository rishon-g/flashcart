package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt" // <-- NEW
	"log/slog"
	"os"
	"strings"
	"time" // <-- NEW

	"flashcart/backend/internal/inventory"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

var db *dynamodb.Client
var productsTable string
var ordersTable string
var adminToken string

func handler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	path := req.RawPath
	method := req.RequestContext.HTTP.Method

	slog.Info("Incoming request", slog.String("method", method), slog.String("path", path))

	// NEW: Catch the secret Chrome Preflight request and say "200 OK"!
	if method == "OPTIONS" {
		return buildResponse(200, map[string]string{"message": "CORS OK"})
	}

	if strings.HasPrefix(path, "/admin/products") && method == "POST" {
		return handleAdminSeed(ctx, req)
	} else if strings.HasPrefix(path, "/products/") && method == "GET" {
		return handleGetProduct(ctx, req)
	} else if strings.HasPrefix(path, "/orders") && method == "POST" {
		return handleCreateOrder(ctx, req)
	} else if strings.HasPrefix(path, "/orders/") && method == "GET" {
		return handleGetOrder(ctx, req)
	}

	return buildResponse(404, map[string]string{"error": "Route not found"})
}

func handleAdminSeed(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	// Only callers holding the admin token (stored in Secrets Manager) may reset stock
	given := req.Headers["x-admin-token"]
	if adminToken == "" || subtle.ConstantTimeCompare([]byte(given), []byte(adminToken)) != 1 {
		return buildResponse(401, map[string]string{"error": "Invalid or missing X-Admin-Token header"})
	}

	_, err := db.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(productsTable),
		Item: map[string]types.AttributeValue{
			"productId": &types.AttributeValueMemberS{Value: "FLASH-TV-001"},
			"stock":     &types.AttributeValueMemberN{Value: "500"},
		},
	})
	if err != nil {
		slog.Error("Seed failed", slog.String("error", err.Error()))
		return buildResponse(500, map[string]string{"error": "Internal Server Error"})
	}
	return buildResponse(201, map[string]string{"message": "Sale Seeded!"})
}

func handleGetProduct(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	parts := strings.Split(req.RawPath, "/")
	productID := parts[len(parts)-1]

	result, err := db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(productsTable),
		Key:       map[string]types.AttributeValue{"productId": &types.AttributeValueMemberS{Value: productID}},
	})
	if err != nil {
		slog.Error("GetItem failed", slog.String("table", productsTable), slog.String("error", err.Error()))
		return buildResponse(500, map[string]string{"error": "Internal Server Error"})
	}

	if result.Item == nil {
		return buildResponse(404, map[string]string{"error": "Product not found"})
	}

	stockAttr, ok := result.Item["stock"].(*types.AttributeValueMemberN)
	if !ok {
		return buildResponse(500, map[string]string{"error": "Internal Server Error"})
	}
	stock := stockAttr.Value
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
		if errors.As(err, &tce) && len(tce.CancellationReasons) == 2 {
			// Check the replay first: a retried key must get 200 even after the sale sells out
			if aws.ToString(tce.CancellationReasons[1].Code) == "ConditionalCheckFailed" {
				return buildResponse(200, map[string]string{"message": "Order already processed (Idempotent replay)"})
			}
			if aws.ToString(tce.CancellationReasons[0].Code) == "ConditionalCheckFailed" {
				// NEW: Emit a metric that a user was rejected because we are sold out!
				logEMFMetric("SoldOutRejections", 1)
				return buildResponse(409, map[string]string{"error": "SOLD_OUT"})
			}
		}
		slog.Error("Reserve failed", slog.String("error", err.Error()))
		return buildResponse(500, map[string]string{"error": "Internal Server Error"})
	}

	return buildResponse(201, map[string]string{"message": "Order placed successfully!"})
}

func handleGetOrder(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	parts := strings.Split(req.RawPath, "/")
	orderID := parts[len(parts)-1]

	result, err := db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(ordersTable),
		Key:       map[string]types.AttributeValue{"orderId": &types.AttributeValueMemberS{Value: orderID}},
	})
	if err != nil {
		slog.Error("GetItem failed", slog.String("table", ordersTable), slog.String("error", err.Error()))
		return buildResponse(500, map[string]string{"error": "Internal Server Error"})
	}
	if result.Item == nil {
		return buildResponse(404, map[string]string{"error": "Order not found"})
	}
	status, ok := result.Item["status"].(*types.AttributeValueMemberS)
	if !ok {
		return buildResponse(500, map[string]string{"error": "Internal Server Error"})
	}
	return buildResponse(200, map[string]string{"orderId": orderID, "status": status.Value})
}

func buildResponse(statusCode int, body map[string]string) (events.APIGatewayV2HTTPResponse, error) {
	jsonBody, _ := json.Marshal(body)
	return events.APIGatewayV2HTTPResponse{
		StatusCode: statusCode,
		Headers: map[string]string{
			"Content-Type": "application/json",
			// NEW: Bulletproof CORS Headers!
			"Access-Control-Allow-Origin":  "*",
			"Access-Control-Allow-Headers": "Content-Type, Idempotency-Key, X-Admin-Token",
			"Access-Control-Allow-Methods": "OPTIONS, POST, GET",
		},
		Body: string(jsonBody),
	}, nil
}

// NEW: The EMF Magic Trick. Printing this specific JSON shape creates a free graph in AWS.
func logEMFMetric(metricName string, value int) {
	timestamp := time.Now().UnixMilli()
	emf := fmt.Sprintf(`{"_aws":{"Timestamp":%d,"CloudWatchMetrics":[{"Namespace":"FlashCart","Dimensions":[[]],"Metrics":[{"Name":"%s"}]}]},"%s":%d}`, timestamp, metricName, metricName, value)
	fmt.Println(emf)
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	productsTable = os.Getenv("PRODUCTS_TABLE")
	ordersTable = os.Getenv("ORDERS_TABLE")

	cfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		slog.Error("Failed to load AWS config", slog.String("error", err.Error()))
		os.Exit(1)
	}
	db = dynamodb.NewFromConfig(cfg)

	// Fetch the admin token once per cold start. If it can't be read, admin calls are rejected.
	if secretArn := os.Getenv("ADMIN_TOKEN_SECRET_ARN"); secretArn != "" {
		secret, err := secretsmanager.NewFromConfig(cfg).GetSecretValue(context.TODO(), &secretsmanager.GetSecretValueInput{SecretId: aws.String(secretArn)})
		if err != nil {
			slog.Error("Failed to read admin token", slog.String("error", err.Error()))
		} else {
			adminToken = aws.ToString(secret.SecretString)
		}
	}

	lambda.Start(handler)
}