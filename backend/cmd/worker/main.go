package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"math/rand"
	"os"
	"time"

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

func init() {
	// Warm start: Connect to the database when the server boots up
	cfg, _ := config.LoadDefaultConfig(context.TODO())
	db = dynamodb.NewFromConfig(cfg)
	productsTable = os.Getenv("PRODUCTS_TABLE")
	ordersTable = os.Getenv("ORDERS_TABLE")
	rand.Seed(time.Now().UnixNano()) // For our random failure simulator
}

func handler(ctx context.Context, sqsEvent events.SQSEvent) (events.SQSEventResponse, error) {
	var failures []events.SQSBatchItemFailure

	// Loop through the messages AWS handed us from the queue
	for _, message := range sqsEvent.Records {
		slog.Info("Processing message", slog.String("messageId", message.MessageId))

		// 1. Extract the Order Data from the EventBridge Pipe JSON
		orderID, productID := parsePipeMessage(message.Body)
		if orderID == "" {
			continue // Skip if we can't parse it
		}

		// 2. SIMULATE PAYMENT PROCESSING
		// Let's pretend 20% of credit cards get declined
		paymentFailed := rand.Float32() < 0.20

		if paymentFailed {
			slog.Warn("Payment declined!", slog.String("orderId", orderID))
			
			// COMPENSATION: The payment failed. We must cancel the order and give the TV back!
			err := refundOrder(ctx, orderID, productID)
			if err != nil {
				slog.Error("Failed to refund order", slog.String("error", err.Error()))
				// Tell AWS this specific message failed so it can retry later
				failures = append(failures, events.SQSBatchItemFailure{ItemIdentifier: message.MessageId})
			}
			continue
		}

		// 3. SUCCESS! Update the order status to CONFIRMED
		err := confirmOrder(ctx, orderID)
		if err != nil {
			slog.Error("Failed to confirm order", slog.String("error", err.Error()))
			failures = append(failures, events.SQSBatchItemFailure{ItemIdentifier: message.MessageId})
		} else {
			slog.Info("Order confirmed successfully!", slog.String("orderId", orderID))
		}
	}

	// 4. Return the list of failures. 
	// AWS will delete the successful ones from the queue, and retry the failed ones!
	return events.SQSEventResponse{BatchItemFailures: failures}, nil
}

// confirmOrder just updates the status from PENDING to CONFIRMED
func confirmOrder(ctx context.Context, orderID string) error {
	_, err := db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(ordersTable),
		Key: map[string]types.AttributeValue{
			"orderId": &types.AttributeValueMemberS{Value: orderID},
		},
		UpdateExpression: aws.String("SET #s = :confirmed"),
		ExpressionAttributeNames: map[string]string{"#s": "status"}, // 'status' is a reserved word in DynamoDB, so we use an alias
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":confirmed": &types.AttributeValueMemberS{Value: "CONFIRMED"},
		},
	})
	return err
}

// refundOrder uses a Transaction to mark it FAILED and add 1 back to the TV stock
func refundOrder(ctx context.Context, orderID string, productID string) error {
	updateOrderOp := &types.TransactWriteItem{
		Update: &types.Update{
			TableName: aws.String(ordersTable),
			Key: map[string]types.AttributeValue{"orderId": &types.AttributeValueMemberS{Value: orderID}},
			UpdateExpression: aws.String("SET #s = :failed"),
			ExpressionAttributeNames: map[string]string{"#s": "status"},
			ExpressionAttributeValues: map[string]types.AttributeValue{":failed": &types.AttributeValueMemberS{Value: "FAILED"}},
		},
	}

	updateStockOp := &types.TransactWriteItem{
		Update: &types.Update{
			TableName: aws.String(productsTable),
			Key: map[string]types.AttributeValue{"productId": &types.AttributeValueMemberS{Value: productID}},
			UpdateExpression: aws.String("SET stock = stock + :qty"), // ADDING IT BACK!
			ExpressionAttributeValues: map[string]types.AttributeValue{":qty": &types.AttributeValueMemberN{Value: "1"}},
		},
	}

	_, err := db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: []types.TransactWriteItem{*updateOrderOp, *updateStockOp},
	})
	return err
}

// Helper to pull the IDs out of the DynamoDB Stream JSON
func parsePipeMessage(body string) (string, string) {
	var payload []struct {
		Dynamodb struct {
			NewImage struct {
				OrderId   struct{ S string } `json:"orderId"`
				ProductId struct{ S string } `json:"productId"`
			} `json:"NewImage"`
		} `json:"dynamodb"`
	}
	json.Unmarshal([]byte(body), &payload)
	if len(payload) > 0 {
		return payload[0].Dynamodb.NewImage.OrderId.S, payload[0].Dynamodb.NewImage.ProductId.S
	}
	return "", ""
}

func main() {
	lambda.Start(handler)
}