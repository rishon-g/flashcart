package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
		
		// a message we can't parse will never succeed, so fail it and let sqs move it to the dlq
		if orderID == "" || productID == "" {
			failures = append(failures, events.SQSBatchItemFailure{ItemIdentifier: message.MessageId})
			continue
		}

		paymentFailed := rand.Float32() < 0.20

		if paymentFailed {
			err := refundOrder(ctx, orderID, productID)
			if isAlreadyProcessed(err) {
				continue // redelivery, the order is no longer pending so don't refund twice
			}
			if err != nil {
				failures = append(failures, events.SQSBatchItemFailure{ItemIdentifier: message.MessageId})
				continue
			}
			// count declined payments
			logEMFMetric("PaymentFailures", 1)
			continue
		}

		err := confirmOrder(ctx, orderID)
		if isAlreadyProcessed(err) {
			continue
		}
		if err != nil {
			failures = append(failures, events.SQSBatchItemFailure{ItemIdentifier: message.MessageId})
		} else {
			// count confirmed orders
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
		// only pending orders can be confirmed, so a redelivered message does nothing
		ConditionExpression: aws.String("#s = :pending"),
		ExpressionAttributeNames: map[string]string{"#s": "status"},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":confirmed": &types.AttributeValueMemberS{Value: "CONFIRMED"},
			":pending":   &types.AttributeValueMemberS{Value: "PENDING"},
		},
	})
	return err
}

// true when the write failed because the order had already left pending
func isAlreadyProcessed(err error) bool {
	var ccf *types.ConditionalCheckFailedException
	if errors.As(err, &ccf) {
		return true
	}
	var tce *types.TransactionCanceledException
	return errors.As(err, &tce) && len(tce.CancellationReasons) > 0 &&
		aws.ToString(tce.CancellationReasons[0].Code) == "ConditionalCheckFailed"
}

func refundOrder(ctx context.Context, orderID string, productID string) error {
	updateOrderOp := &types.TransactWriteItem{
		Update: &types.Update{TableName: aws.String(ordersTable), Key: map[string]types.AttributeValue{"orderId": &types.AttributeValueMemberS{Value: orderID}}, UpdateExpression: aws.String("SET #s = :failed"), ConditionExpression: aws.String("#s = :pending"), ExpressionAttributeNames: map[string]string{"#s": "status"}, ExpressionAttributeValues: map[string]types.AttributeValue{":failed": &types.AttributeValueMemberS{Value: "FAILED"}, ":pending": &types.AttributeValueMemberS{Value: "PENDING"}}},
	}
	updateStockOp := &types.TransactWriteItem{
		Update: &types.Update{TableName: aws.String(productsTable), Key: map[string]types.AttributeValue{"productId": &types.AttributeValueMemberS{Value: productID}}, UpdateExpression: aws.String("SET stock = stock + :qty"), ExpressionAttributeValues: map[string]types.AttributeValue{":qty": &types.AttributeValueMemberN{Value: "1"}}},
	}
	_, err := db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{*updateOrderOp, *updateStockOp}})
	return err
}

func parsePipeMessage(body string) (string, string) {
	// pipes sends one stream record per message, not an array
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

// writes a metric to stdout in cloudwatch emf format
func logEMFMetric(metricName string, value int) {
	timestamp := time.Now().UnixMilli()
	emf := fmt.Sprintf(`{"_aws":{"Timestamp":%d,"CloudWatchMetrics":[{"Namespace":"FlashCart","Dimensions":[[]],"Metrics":[{"Name":"%s"}]}]},"%s":%d}`, timestamp, metricName, metricName, value)
	fmt.Println(emf)
}

func main() { lambda.Start(handler) }