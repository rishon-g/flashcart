package main

import (
	"context"
	"encoding/json"
	"fmt" // <-- NEW
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
	cfg, _ := config.LoadDefaultConfig(context.TODO())
	db = dynamodb.NewFromConfig(cfg)
	productsTable = os.Getenv("PRODUCTS_TABLE")
	ordersTable = os.Getenv("ORDERS_TABLE")
	rand.Seed(time.Now().UnixNano())
}

func handler(ctx context.Context, sqsEvent events.SQSEvent) (events.SQSEventResponse, error) {
	var failures []events.SQSBatchItemFailure

	for _, message := range sqsEvent.Records {
		orderID, productID := parsePipeMessage(message.Body)
		fmt.Println("RAW MESSAGE:", message.Body)
		
		if orderID == "" {
			continue
		}

		paymentFailed := rand.Float32() < 0.20

		if paymentFailed {
			// NEW: Emit metric that a payment failed!
			logEMFMetric("PaymentFailures", 1)
			err := refundOrder(ctx, orderID, productID)
			if err != nil {
				failures = append(failures, events.SQSBatchItemFailure{ItemIdentifier: message.MessageId})
			}
			continue
		}

		err := confirmOrder(ctx, orderID)
		if err != nil {
			failures = append(failures, events.SQSBatchItemFailure{ItemIdentifier: message.MessageId})
		} else {
			// NEW: Emit metric that a purchase fully succeeded!
			logEMFMetric("OrdersPlaced", 1)
		}
	}
	return events.SQSEventResponse{BatchItemFailures: failures}, nil
}

func confirmOrder(ctx context.Context, orderID string) error {
	_, err := db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(ordersTable),
		Key:       map[string]types.AttributeValue{"orderId": &types.AttributeValueMemberS{Value: orderID}},
		UpdateExpression: aws.String("SET #s = :confirmed"),
		ExpressionAttributeNames: map[string]string{"#s": "status"},
		ExpressionAttributeValues: map[string]types.AttributeValue{":confirmed": &types.AttributeValueMemberS{Value: "CONFIRMED"}},
	})
	return err
}

func refundOrder(ctx context.Context, orderID string, productID string) error {
	updateOrderOp := &types.TransactWriteItem{
		Update: &types.Update{TableName: aws.String(ordersTable), Key: map[string]types.AttributeValue{"orderId": &types.AttributeValueMemberS{Value: orderID}}, UpdateExpression: aws.String("SET #s = :failed"), ExpressionAttributeNames: map[string]string{"#s": "status"}, ExpressionAttributeValues: map[string]types.AttributeValue{":failed": &types.AttributeValueMemberS{Value: "FAILED"}}},
	}
	updateStockOp := &types.TransactWriteItem{
		Update: &types.Update{TableName: aws.String(productsTable), Key: map[string]types.AttributeValue{"productId": &types.AttributeValueMemberS{Value: productID}}, UpdateExpression: aws.String("SET stock = stock + :qty"), ExpressionAttributeValues: map[string]types.AttributeValue{":qty": &types.AttributeValueMemberN{Value: "1"}}},
	}
	_, err := db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{*updateOrderOp, *updateStockOp}})
	return err
}

func parsePipeMessage(body string) (string, string) {
	// Notice we removed the [] brackets! It's just a single struct now.
	var payload struct {
		Dynamodb struct {
			NewImage struct {
				OrderId   struct{ S string } `json:"orderId"`
				ProductId struct{ S string } `json:"productId"`
			} `json:"NewImage"`
		} `json:"dynamodb"`
	}
	
	err := json.Unmarshal([]byte(body), &payload)
	if err != nil {
		fmt.Println("JSON Parse Error:", err)
		return "", ""
	}
	
	return payload.Dynamodb.NewImage.OrderId.S, payload.Dynamodb.NewImage.ProductId.S
}

// NEW: EMF Metric helper for the worker
func logEMFMetric(metricName string, value int) {
	timestamp := time.Now().UnixMilli()
	emf := fmt.Sprintf(`{"_aws":{"Timestamp":%d,"CloudWatchMetrics":[{"Namespace":"FlashCart","Dimensions":[[]],"Metrics":[{"Name":"%s"}]}]},"%s":%d}`, timestamp, metricName, metricName, value)
	fmt.Println(emf)
}

func main() { lambda.Start(handler) }